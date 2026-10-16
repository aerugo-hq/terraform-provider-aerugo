# Every team's sources, from one reviewed repository: teams/<team>/sources.yaml, one file per team, each owned by that
# team (CODEOWNERS). CI plans on every pull request and applies on merge, signed in as a service account. Nothing
# secret lives here: each source names a credential_ref, a secret the team keeps. Adding a team is adding its file.
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
  teams = {
    for file in fileset(path.module, "teams/*/sources.yaml") :
    basename(dirname(file)) => yamldecode(file("${path.module}/${file}"))
  }
}

module "team" {
  source   = "./modules/aerugo-team"
  for_each = local.teams

  team        = each.key
  connections = try(each.value.connections, [])
}

output "sources" {
  description = "Every team's sources, with their status."
  value       = { for team, m in module.team : team => m.connections }
}
