package provider

import (
	"fmt"
	"testing"

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
