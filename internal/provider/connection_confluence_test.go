package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// A Confluence connection, against the development stack's fake Confluence (smoke runs it): its spaces, labels, and
// page trees change in place, and its token, write-only, is never in state.
func TestAccConnectionConfluence(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(spaces, labels string) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Confluence"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_connection" "wiki" {
  team          = aerugo_team.t.slug
  name          = "wiki"
  kind          = "confluence"
  site          = "http://fake-llm:8080/confluence"
  email         = "confluence-bot@acme.test"
  api_token     = "smoke-confluence-token"
  spaces        = %s
  labels        = %s
  every_minutes = 60
}
`, slug, spaces, labels)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config(`["PAY"]`, `["runbook"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.wiki", "id", slug+"/wiki"),
					resource.TestCheckResourceAttr("aerugo_connection.wiki", "kind", "confluence"),
					resource.TestCheckResourceAttr("aerugo_connection.wiki", "spaces.#", "1"),
					resource.TestCheckResourceAttr("aerugo_connection.wiki", "labels.0", "runbook"),
					resource.TestCheckResourceAttr("aerugo_connection.wiki", "company_site", "false"),
					resource.TestCheckNoResourceAttr("aerugo_connection.wiki", "api_token"), // write-only: never in state
				),
			},
			{
				ResourceName:            "aerugo_connection.wiki",
				ImportState:             true,
				ImportStateId:           slug + "/wiki",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_token", "service_account_key", "email", "credential_version", "status"},
			},
			{ // another space and another label: in place
				Config: config(`["PAY", "ENG"]`, `["runbook", "decision"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_connection.wiki", "spaces.#", "2"),
					resource.TestCheckResourceAttr("aerugo_connection.wiki", "labels.#", "2"),
				),
			},
		},
	})
}
