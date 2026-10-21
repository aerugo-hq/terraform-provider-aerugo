package provider

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The connection points at the development stack's fake Jira (smoke runs it), so connecting it calls no real
// Atlassian site. Its token is a write-only argument: sent once, never in state.
func TestAccConnection(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(every int, projects string, enabled bool) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Connections"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "jira" {
  team          = aerugo_team.t.slug
  name          = "jira"
  kind          = "jira"
  site          = "http://fake-llm:8080/jira"
  email         = "jira-bot@acme.test"
  api_token     = "smoke-jira-token"
  projects      = %s
  every_minutes = %d
  enabled       = %t
}
`, slug, projects, every, enabled)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config(60, `["OPS"]`, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.jira", "id", slug+"/jira"),
					resource.TestCheckResourceAttr("aerugo_connection.jira", "kind", "jira"),
					resource.TestCheckResourceAttr("aerugo_connection.jira", "site", "http://fake-llm:8080/jira"),
					resource.TestCheckResourceAttr("aerugo_connection.jira", "every_minutes", "60"),
					resource.TestCheckResourceAttr("aerugo_connection.jira", "projects.#", "1"),
					resource.TestCheckResourceAttr("aerugo_connection.jira", "enabled", "true"),
					resource.TestCheckNoResourceAttr("aerugo_connection.jira", "api_token"), // write-only: never in state
					resource.TestCheckNoResourceAttr("aerugo_connection.jira", "credential_ref"),
				),
			},
			{
				ResourceName:            "aerugo_connection.jira",
				ImportState:             true,
				ImportStateId:           slug + "/jira",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_token", "service_account_key", "email", "credential_version", "status"},
			},
			{ // a new cadence, another project, paused: all in place
				Config: config(30, `["OPS", "EDP"]`, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.jira", "every_minutes", "30"),
					resource.TestCheckResourceAttr("aerugo_connection.jira", "projects.#", "2"),
					resource.TestCheckResourceAttr("aerugo_connection.jira", "enabled", "false"),
					resource.TestCheckResourceAttr("aerugo_connection.jira", "status", "disabled"),
				),
			},
		},
	})
}

// A Google Workspace directory (S2) against the development stack's fake Admin SDK: its key is write-only, the admin it
// acts as changes in place, and Google's own address isn't read back as drift. The key is a stand-in that only has a
// service account's shape; the worker can't sign with it, and the test doesn't need it to.
func TestAccConnectionGoogleDirectory(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(admin string) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Directory"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "workspace" {
  team                = aerugo_team.t.slug
  name                = "workspace"
  kind                = "directory"
  directory           = "google"
  site                = "http://fake-llm:8080/google"
  admin_email         = %q
  service_account_key = jsonencode({
    type         = "service_account"
    client_email = "tfacc-directory@acme-dev.iam.gserviceaccount.com"
    private_key  = "-----BEGIN PRIVATE KEY-----\nnot-a-real-key\n-----END PRIVATE KEY-----\n"
  })
  every_minutes = 60
}
`, slug, admin)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config("directory-admin@acme.test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.workspace", "kind", "directory"),
					resource.TestCheckResourceAttr("aerugo_connection.workspace", "directory", "google"),
					resource.TestCheckResourceAttr("aerugo_connection.workspace", "admin_email", "directory-admin@acme.test"),
					resource.TestCheckNoResourceAttr("aerugo_connection.workspace", "service_account_key"), // write-only
					resource.TestCheckNoResourceAttr("aerugo_connection.workspace", "credential_ref"),
				),
			},
			{
				ResourceName:            "aerugo_connection.workspace",
				ImportState:             true,
				ImportStateId:           slug + "/workspace",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"service_account_key", "site", "credential_version", "status"},
			},
			{ // another admin to act as: in place
				Config: config("directory-reader@acme.test"),
				Check:  resource.TestCheckResourceAttr("aerugo_connection.workspace", "admin_email", "directory-reader@acme.test"),
			},
		},
	})
}

// testCertificate is a throwaway RSA key and self-signed certificate, as PEM: the shape an Entra app's certificate
// credential takes. The API checks the key is the certificate's; nothing signs in with it here.
func testCertificate(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "tfacc-entra"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// An Entra directory (S3): its tenant and app are read back, its certificate is write-only, and Graph's own address
// isn't read back as drift.
func TestAccConnectionEntraDirectory(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	tenant, app := "8b3e9a64-7c1f-4f3e-9d2a-1a2b3c4d5e6f", "2f6d1c8e-5a4b-4c3d-8e9f-0a1b2c3d4e5f"
	config := fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Entra"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "entra" {
  team          = aerugo_team.t.slug
  name          = "entra"
  kind          = "directory"
  directory     = "entra"
  site          = "http://fake-llm:8080/microsoft"
  tenant_id     = %q
  client_id     = %q
  certificate   = <<-EOT
%s
  EOT
  every_minutes = 60
}
`, slug, tenant, app, testCertificate(t))
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.entra", "directory", "entra"),
					resource.TestCheckResourceAttr("aerugo_connection.entra", "tenant_id", tenant),
					resource.TestCheckResourceAttr("aerugo_connection.entra", "client_id", app),
					resource.TestCheckNoResourceAttr("aerugo_connection.entra", "certificate"), // write-only
				),
			},
			{
				ResourceName:            "aerugo_connection.entra",
				ImportState:             true,
				ImportStateId:           slug + "/entra",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"certificate", "site", "credential_version", "status"},
			},
		},
	})
}

// A Slack connection (S4) against the development stack's fake Slack: its bot token is write-only, and its channels
// and history window change in place.
func TestAccConnectionSlack(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(channels string, days int) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Slack"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "slack" {
  team          = aerugo_team.t.slug
  name          = "slack"
  kind          = "slack"
  site          = "http://fake-llm:8080/slack/api"
  api_token     = "xoxb-smoke-slack-token"
  channels      = %s
  history_days  = %d
  every_minutes = 60
}
`, slug, channels, days)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config(`["C0123ABCD"]`, 30),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.slack", "kind", "slack"),
					resource.TestCheckResourceAttr("aerugo_connection.slack", "channels.#", "1"),
					resource.TestCheckResourceAttr("aerugo_connection.slack", "history_days", "30"),
					resource.TestCheckResourceAttr("aerugo_connection.slack", "company_workspace", "false"),
					resource.TestCheckNoResourceAttr("aerugo_connection.slack", "api_token"), // write-only
				),
			},
			{
				ResourceName:            "aerugo_connection.slack",
				ImportState:             true,
				ImportStateId:           slug + "/slack",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_token", "site", "credential_version", "status"},
			},
			{ // another channel, a longer window: in place
				Config: config(`["C0123ABCD", "C0456EFGH"]`, 90),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.slack", "channels.#", "2"),
					resource.TestCheckResourceAttr("aerugo_connection.slack", "history_days", "90"),
				),
			},
		},
	})
}

// A Microsoft 365 connection (S5): its tenant, app, and granted sites are read back, its certificate is write-only,
// and its sites change in place.
func TestAccConnectionMicrosoft365(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	tenant, app := "8b3e9a64-7c1f-4f3e-9d2a-1a2b3c4d5e6f", "2f6d1c8e-5a4b-4c3d-8e9f-0a1b2c3d4e5f"
	one := "acme.sharepoint.com,11111111-2222-3333-4444-555555555555,66666666-7777-8888-9999-000000000000"
	two := "acme.sharepoint.com,11111111-2222-3333-4444-555555555556,66666666-7777-8888-9999-000000000001"
	certificate := testCertificate(t)
	config := func(sites string) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Microsoft"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "m365" {
  team          = aerugo_team.t.slug
  name          = "m365"
  kind          = "microsoft"
  site          = "http://fake-llm:8080/microsoft"
  tenant_id     = %q
  client_id     = %q
  sites         = %s
  certificate   = <<-EOT
%s
  EOT
  every_minutes = 60
}
`, slug, tenant, app, sites, certificate)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config(fmt.Sprintf("[%q]", one)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.m365", "kind", "microsoft"),
					resource.TestCheckResourceAttr("aerugo_connection.m365", "tenant_id", tenant),
					resource.TestCheckResourceAttr("aerugo_connection.m365", "sites.#", "1"),
					resource.TestCheckNoResourceAttr("aerugo_connection.m365", "certificate"), // write-only
				),
			},
			{
				ResourceName:            "aerugo_connection.m365",
				ImportState:             true,
				ImportStateId:           slug + "/m365",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"certificate", "site", "credential_version", "status"},
			},
			{ // another site granted: in place
				Config: config(fmt.Sprintf("[%q, %q]", one, two)),
				Check:  resource.TestCheckResourceAttr("aerugo_connection.m365", "sites.#", "2"),
			},
		},
	})
}

// A Teams connection (C1a): its tenant, app, teams, and channels are read back, its certificate is write-only, and a
// team added changes it in place.
func TestAccConnectionTeams(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	tenant, app := "8b3e9a64-7c1f-4f3e-9d2a-1a2b3c4d5e6f", "3a7e2d9f-6b5c-4d4e-9f0a-1b2c3d4e5f60"
	one, two := "11111111-2222-3333-4444-555555555555", "11111111-2222-3333-4444-555555555556"
	certificate := testCertificate(t)
	config := func(teams string) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Teams"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "teams" {
  team          = aerugo_team.t.slug
  name          = "teams"
  kind          = "teams"
  site          = "http://fake-llm:8080/microsoft"
  bot_service   = "http://fake-llm:8080/teams-bot"
  tenant_id     = %q
  client_id     = %q
  team_ids      = %s
  channels      = ["19:handover@thread.tacv2"]
  history_days  = 90
  certificate   = <<-EOT
%s
  EOT
  every_minutes = 60
}
`, slug, tenant, app, teams, certificate)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config(fmt.Sprintf("[%q]", one)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.teams", "kind", "teams"),
					resource.TestCheckResourceAttr("aerugo_connection.teams", "tenant_id", tenant),
					resource.TestCheckResourceAttr("aerugo_connection.teams", "team_ids.#", "1"),
					resource.TestCheckResourceAttr("aerugo_connection.teams", "channels.0", "19:handover@thread.tacv2"),
					resource.TestCheckResourceAttr("aerugo_connection.teams", "history_days", "90"),
					resource.TestCheckResourceAttr("aerugo_connection.teams", "bot_service", "http://fake-llm:8080/teams-bot"),
					resource.TestCheckNoResourceAttr("aerugo_connection.teams", "certificate"), // write-only
				),
			},
			{
				ResourceName:            "aerugo_connection.teams",
				ImportState:             true,
				ImportStateId:           slug + "/teams",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"certificate", "site", "credential_version", "status"},
			},
			{ // another team: in place
				Config: config(fmt.Sprintf("[%q, %q]", one, two)),
				Check:  resource.TestCheckResourceAttr("aerugo_connection.teams", "team_ids.#", "2"),
			},
		},
	})
}

// A live Notion connection (S6) against the development stack's fake Notion: its integration token is write-only.
func TestAccConnectionCatalog(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(catalogs string) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Catalogs"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "unity" {
  team          = aerugo_team.t.slug
  name          = "unity"
  kind          = "catalog"
  catalog       = "unity"
  site          = "http://fake-llm:8080/databricks"
  client_id     = "0b1e6a4e-6d0c-4c7e-9a43-5f2d8c1e7a10"
  api_token     = "dbx-smoke-oauth-secret"
  catalogs      = %s
  every_minutes = 1440
}

resource "aerugo_connection" "dataplex" {
  team     = aerugo_team.t.slug
  name     = "dataplex"
  kind     = "catalog"
  catalog  = "dataplex"
  site     = "http://fake-llm:8080/gcp-catalog"
  projects = ["acme-data"]
  domain   = "acme.test"
}
`, slug, catalogs)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config(`["main"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.unity", "catalog", "unity"),
					resource.TestCheckResourceAttr("aerugo_connection.unity", "catalogs.0", "main"),
					resource.TestCheckNoResourceAttr("aerugo_connection.unity", "api_token"), // write-only
					resource.TestCheckResourceAttr("aerugo_connection.dataplex", "projects.0", "acme-data"),
					resource.TestCheckResourceAttr("aerugo_connection.dataplex", "location", "global"),
					resource.TestCheckNoResourceAttr("aerugo_connection.dataplex", "credential_ref"), // read as the runner itself
				),
			},
			{
				Config: config(`["main", "finance"]`), // a scope list changes in place
				Check:  resource.TestCheckResourceAttr("aerugo_connection.unity", "catalogs.1", "finance"),
			},
			{
				ResourceName:            "aerugo_connection.unity",
				ImportState:             true,
				ImportStateId:           slug + "/unity",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_token", "credential_version", "status"},
			},
		},
	})
}

func TestAccConnectionNotion(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Notion"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "notion" {
  team          = aerugo_team.t.slug
  name          = "notion"
  kind          = "notion"
  site          = "http://fake-llm:8080/notion/v1"
  api_token     = "ntn_smoke_notion_integration_token"
  every_minutes = 60
}
`, slug),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.notion", "kind", "notion"),
					resource.TestCheckResourceAttr("aerugo_connection.notion", "every_minutes", "60"),
					resource.TestCheckNoResourceAttr("aerugo_connection.notion", "api_token"), // write-only
				),
			},
			{
				ResourceName:            "aerugo_connection.notion",
				ImportState:             true,
				ImportStateId:           slug + "/notion",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_token", "site", "credential_version", "status"},
			},
		},
	})
}
