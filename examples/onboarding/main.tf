# Every team, from one reviewed repository: teams/<team>/team.yaml (the team and who is on it, the platform team's to
# approve) and teams/<team>/sources.yaml (its sources, the team's own), and org.yaml (who gets in). CI plans on every
# pull request and applies on merge, signed in as a service account. Onboarding a team is a pull request adding its
# folder; a pilot (org.yaml's admission: teams) lets in only the teams here, a wave at a time. Nothing secret lives
# here: each source names a credential_ref, a secret the team keeps.
terraform {
  required_version = ">= 1.11"

  required_providers {
    aerugo = {
      source = "aerugo-hq/aerugo" # installed from your private mirror: see terraformrc.example
    }
  }

  # State lives where every CI run finds it, or each run would try to connect everything again. A GCS bucket here;
  # any remote backend works. CI signs in to it with workload identity, so no key is stored for it either.
  backend "gcs" {
    bucket = "acme-aerugo-onboarding-state"
    prefix = "onboarding"
  }
}

provider "aerugo" {
  managed_workspace = "onboarding" # shown in the app beside every source this repository manages
}

locals {
  folders = toset([for file in fileset(path.module, "teams/*/{team,sources}.yaml") : basename(dirname(file))])
  org     = fileexists("${path.module}/org.yaml") ? yamldecode(file("${path.module}/org.yaml")) : {}
}

# Who gets in: "teams" while a pilot runs (only members of the teams below, and org admins), "org" once everyone is in.
resource "aerugo_admission" "org" {
  count = try(local.org.admission, null) == null ? 0 : 1
  mode  = local.org.admission
}

module "team" {
  source   = "./modules/aerugo-team"
  for_each = local.folders

  team        = each.key
  settings    = try(yamldecode(file("${path.module}/teams/${each.key}/team.yaml")), null) # none: the team exists already
  connections = try(yamldecode(file("${path.module}/teams/${each.key}/sources.yaml")).connections, [])
}

output "sources" {
  description = "Every team's sources, with their status."
  value       = { for team, m in module.team : team => m.connections }
}
