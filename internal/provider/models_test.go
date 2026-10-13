package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The model points at the development stack's stand-in model, so registering it calls no real provider.
func TestAccModelAndBudget(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(model string, keyVersion, budget int) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = "Models"
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_model" "m" {
  team                    = aerugo_team.t.slug
  name                    = "assistant"
  api                     = "openai_compatible"
  model                   = %q
  base_url                = "http://fake-llm:8080/v1"
  api_key                 = "unused"
  api_key_version         = %d
  allowed_classifications = ["public", "internal"]
}

resource "aerugo_model_budget" "m" {
  team           = aerugo_team.t.slug
  model          = aerugo_model.m.name
  monthly_tokens = %d
}
`, slug, model, keyVersion, budget)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config("fake", 1, 5000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_model.m", "id", slug+"/assistant"),
					resource.TestCheckNoResourceAttr("aerugo_model.m", "api_key"), // write-only: never in state
					resource.TestCheckResourceAttr("aerugo_model.m", "allowed_classifications.#", "2"),
					resource.TestCheckResourceAttr("aerugo_model_budget.m", "monthly_tokens", "5000"),
				),
			},
			{
				ResourceName:            "aerugo_model.m",
				ImportState:             true,
				ImportStateId:           slug + "/assistant",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_key", "api_key_version"},
			},
			{
				ResourceName:      "aerugo_model_budget.m",
				ImportState:       true,
				ImportStateId:     slug + "/assistant",
				ImportStateVerify: true,
			},
			{ // a new model name, a rotated key, a bigger budget: all in place
				Config: config("fake-2", 2, 9000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_model.m", "model", "fake-2"),
					resource.TestCheckResourceAttr("aerugo_model_budget.m", "monthly_tokens", "9000"),
				),
			},
		},
	})
}

// The client needn't exist in the identity provider: registering an account only says its tokens may be accepted.
func TestAccServiceAccount(t *testing.T) {
	id := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(name string, enabled, orgAdmin bool) string {
		return fmt.Sprintf(`
resource "aerugo_service_account" "ci" {
  client_id = %q
  name      = %q
  enabled   = %t
  org_admin = %t
}
`, id, name, enabled, orgAdmin)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config("CI pipeline", true, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_service_account.ci", "principal", "user:service-account-"+id),
					resource.TestCheckResourceAttr("aerugo_service_account.ci", "org_admin", "false"),
				),
			},
			{ResourceName: "aerugo_service_account.ci", ImportState: true, ImportStateId: id, ImportStateVerify: true},
			{
				Config: config("CI pipeline (paused)", false, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_service_account.ci", "enabled", "false"),
					resource.TestCheckResourceAttr("aerugo_service_account.ci", "org_admin", "true"),
				),
			},
		},
	})
}
