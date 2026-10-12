package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

// Acceptance tests run only with TF_ACC=1, against a running Aerugo (make up in the platform's repository) and a
// registered service account (scripts/dev-service-account.ps1). See the README.
var testAccProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"aerugo": providerserver.NewProtocol6WithError(New("test")()),
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	for _, env := range []string{"AERUGO_ENDPOINT", "AERUGO_TOKEN_URL", "AERUGO_CLIENT_ID", "AERUGO_CLIENT_SECRET"} {
		if os.Getenv(env) == "" {
			t.Fatalf("%s must be set for acceptance tests; see the README", env)
		}
	}
}

// changeByHand renames a team the way the app does, outside Terraform, so the next plan must show the drift.
func changeByHand(t *testing.T, slug, name string) {
	t.Helper()
	c, err := client.New(client.Config{
		Endpoint: os.Getenv("AERUGO_ENDPOINT"), TokenURL: os.Getenv("AERUGO_TOKEN_URL"),
		ClientID: os.Getenv("AERUGO_CLIENT_ID"), ClientSecret: os.Getenv("AERUGO_CLIENT_SECRET"),
		UserAgent: "terraform-provider-aerugo/acceptance-test", Unmanaged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), http.MethodPatch, "/v1/workspaces/"+url.PathEscape(slug), nil, map[string]string{"name": name}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAccTeamMembersImportAndDrift(t *testing.T) {
	slug := "tfacc-" + acctest.RandStringFromCharSet(6, acctest.CharSetAlphaNum)
	config := func(name, discoverability string) string {
		return fmt.Sprintf(`
resource "aerugo_team" "t" {
  slug                = %q
  name                = %q
  discoverability     = %q
  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_team_member" "carol" {
  team      = aerugo_team.t.slug
  principal = "user:carol"
  role      = "member"
}

data "aerugo_team" "t" {
  slug       = aerugo_team.t.slug
  depends_on = [aerugo_team_member.carol]
}
`, slug, name, discoverability)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{ // create: and the app knows Terraform manages it
				Config: config("Acceptance", "org"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_team.t", "id", slug),
					resource.TestCheckResourceAttr("aerugo_team.t", "discoverability", "org"),
					resource.TestCheckResourceAttr("aerugo_team_member.carol", "id", slug+"/user:carol/member"),
					resource.TestCheckResourceAttr("data.aerugo_team.t", "name", "Acceptance"),
					resource.TestCheckResourceAttr("data.aerugo_team.t", "managed_by", "terraform"),
				),
			},
			{ // import: what was made by clicking can be adopted
				ResourceName:            "aerugo_team.t",
				ImportState:             true,
				ImportStateId:           slug,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"deletion_protection", "wait_for_purge"},
			},
			{
				ResourceName:      "aerugo_team_member.carol",
				ImportState:       true,
				ImportStateId:     slug + "/user:carol/member",
				ImportStateVerify: true,
			},
			{ // update in place
				Config: config("Renamed", "private"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_team.t", "name", "Renamed"),
					resource.TestCheckResourceAttr("aerugo_team.t", "discoverability", "private"),
				),
			},
			{ // drift: a change made in the app shows in the next plan
				PreConfig:          func() { changeByHand(t, slug, "Changed in the app") },
				Config:             config("Renamed", "private"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}
