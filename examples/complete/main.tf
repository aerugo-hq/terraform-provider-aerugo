# A whole org from scratch: apply it against your local Aerugo, then open the app and find it all there, marked
# "Managed by Terraform". It grows with the provider: models and budgets next, then sources and rules.
#
# The provider reads its settings from AERUGO_* (scripts/dev-service-account.ps1 prints them), so nothing secret is
# written here.

terraform {
  required_providers {
    aerugo = {
      source = "aerugo-hq/aerugo"
    }
  }
}

provider "aerugo" {
  managed_workspace = "examples-complete" # shown in the app beside everything this configuration manages
}

data "aerugo_status" "now" {}

resource "aerugo_team" "support" {
  slug = "support"
  name = "Customer support"

  deletion_protection = false # an example: real teams keep the default, true
  wait_for_purge      = true
}

resource "aerugo_team" "security" {
  slug            = "security"
  name            = "Security"
  discoverability = "private" # members only

  deletion_protection = false
  wait_for_purge      = true
}

resource "aerugo_team_member" "support_lead" {
  team      = aerugo_team.support.slug
  principal = "user:alice"
  role      = "admin"
}

resource "aerugo_team_member" "support_everyone" {
  team      = aerugo_team.support.slug
  principal = "group:payments" # a whole group, from your identity provider
  role      = "member"
}

resource "aerugo_team_member" "security_reviewer" {
  team      = aerugo_team.security.slug
  principal = "user:carol"
  role      = "auditor"
}

output "aerugo_version" {
  value = data.aerugo_status.now.version
}

output "teams" {
  value = [aerugo_team.support.id, aerugo_team.security.id]
}
