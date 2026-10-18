package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// A pilot (G4): admission goes to "teams" and back to "org" in place, imports as "admission", and removing it lets
// everyone signed in back in. The provider signs in as a service account, which the gate never refuses.
func TestAccAdmission(t *testing.T) {
	config := func(mode string) string {
		return fmt.Sprintf(`
resource "aerugo_admission" "pilot" {
  mode = %q
}
`, mode)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config("teams"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("aerugo_admission.pilot", "id", "admission"),
					resource.TestCheckResourceAttr("aerugo_admission.pilot", "mode", "teams"),
				),
			},
			{ResourceName: "aerugo_admission.pilot", ImportState: true, ImportStateId: "admission", ImportStateVerify: true},
			{
				Config: config("org"), // the pilot ends, in place
				Check:  resource.TestCheckResourceAttr("aerugo_admission.pilot", "mode", "org"),
			},
		},
	})
}
