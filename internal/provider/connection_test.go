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
