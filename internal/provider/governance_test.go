package provider

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccPipelineAndComplianceRules(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(keep int) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Governance"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_pipeline" "docs" {
  team       = aerugo_team.t.slug
  name       = "docs-ci"
  issuer     = "https://token.actions.githubusercontent.com"
  repository = "acme/%s-docs"
  ref        = "refs/heads/main"
}

resource "aerugo_retention_policy" "drafts" {
  team           = aerugo_team.t.slug
  target         = "documents"
  classification = "internal"
  keep_days      = %d
}

resource "aerugo_legal_hold" "audit" {
  team   = aerugo_team.t.slug
  reason = "Acceptance test hold"
}
`, slug, slug, keep)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config(90),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_pipeline.docs", "id", slug+"/docs-ci"),
					resource.TestCheckResourceAttr("aerugo_retention_policy.drafts", "keep_days", "90"),
					resource.TestCheckResourceAttrSet("aerugo_legal_hold.audit", "id"),
				),
			},
			{
				Config: config(30), // the days change in place
				Check:  resource.TestCheckResourceAttr("aerugo_retention_policy.drafts", "keep_days", "30"),
			},
			{ResourceName: "aerugo_pipeline.docs", ImportState: true, ImportStateId: slug + "/docs-ci", ImportStateVerify: true},
			{ResourceName: "aerugo_retention_policy.drafts", ImportState: true, ImportStateVerify: true},
			{ResourceName: "aerugo_legal_hold.audit", ImportState: true, ImportStateVerify: true},
		},
	})
}

// A route needs a GitHub connection with a repository to send elsewhere, which only a real GitHub App gives:
// AERUGO_TEST_ROUTE names one as team/connection/owner/repository, and another team to send it to.
func TestAccConnectionRoute(t *testing.T) {
	spec, to := os.Getenv("AERUGO_TEST_ROUTE"), os.Getenv("AERUGO_TEST_ROUTE_TO")
	parts := strings.SplitN(spec, "/", 4)
	if len(parts) != 4 || to == "" {
		t.Skip("set AERUGO_TEST_ROUTE (team/connection/owner/repository) and AERUGO_TEST_ROUTE_TO to test routes against a real GitHub connection")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "aerugo_connection_route" "r" {
  team       = %q
  connection_name = %q
  repository = %q
  to_team    = %q
}
`, parts[0], parts[1], parts[2]+"/"+parts[3], to),
				Check: resource.TestCheckResourceAttr("aerugo_connection_route.r", "to_team", to),
			},
			{ResourceName: "aerugo_connection_route.r", ImportState: true, ImportStateId: spec, ImportStateVerify: true},
		},
	})
}
