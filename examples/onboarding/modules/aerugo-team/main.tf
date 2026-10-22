# One team, as its folder declares it: teams/<team>/team.yaml, the team and who is on it (an aerugo_team, and an
# aerugo_team_member for each role holder, a person or an identity provider's group), and teams/<team>/sources.yaml,
# its sources (each an aerugo_connection). The platform team owns this module and every team.yaml (CODEOWNERS: it
# decides who's onboarded); each team owns its sources.yaml. No secret ever lives here: every source but GitHub (which
# reads through the GitHub App) names a credential_ref, a secret the team keeps in the secrets backend.
terraform {
  required_providers {
    aerugo = {
      source = "aerugo-hq/aerugo"
    }
  }
}

variable "team" {
  description = "The team's slug (its folder's name)."
  type        = string
}

variable "settings" {
  description = "The team and who is on it, from its team.yaml; null when the folder has none (the team exists already)."
  type = object({
    name            = string
    discoverability = optional(string, "private") # private, or org: anyone in the org may find and join it
    admins          = optional(list(string), [])  # each user:<username> or group:<name or id>
    curators        = optional(list(string), [])
    members         = optional(list(string), [])
    auditors        = optional(list(string), [])
  })
  default = null

  validation {
    condition = var.settings == null || alltrue([for p in concat(var.settings.admins, var.settings.curators, var.settings.members, var.settings.auditors) :
    can(regex("^(user|group):[A-Za-z0-9][A-Za-z0-9._@-]{0,127}$", p))])
    error_message = "Each person is user:<username> and each group group:<name or id>, from the identity provider."
  }
}

variable "connections" {
  description = "The team's sources, from its sources.yaml."
  type = list(object({
    name              = string
    kind              = string           # github, google, jira, confluence, slack, microsoft, teams, notion, catalog, or directory
    site              = optional(string) # Jira's or Confluence's site, or a directory's Okta org
    projects          = optional(list(string))
    spaces            = optional(list(string)) # Confluence: its spaces, by key
    labels            = optional(list(string)) # Confluence: only pages with one of these labels
    page_trees        = optional(list(string)) # Confluence: only these pages, by id, and what's under them
    company_site      = optional(bool, false)
    installation_id   = optional(number)
    repositories      = optional(list(string))
    channels          = optional(list(string)) # Slack, Teams: its channels, by id
    sites             = optional(list(string)) # Microsoft 365: its granted sites, by Graph's site id
    team_ids          = optional(list(string)) # Teams: the teams it may read, by id
    bot_service       = optional(string)       # Teams: its bot's Bot Framework address; unset, Teams' global one
    company_workspace = optional(bool, false)  # Slack: the company's own workspace
    history_days      = optional(number)       # Slack, Teams: how far back it reads
    drives            = optional(list(string))
    domain            = optional(string)
    directory         = optional(string)       # okta, google (Google Workspace), or entra
    admin_email       = optional(string)       # Google Workspace: the read-only admin it acts as
    tenant_id         = optional(string)       # Entra, Microsoft 365, Teams: the tenant's id
    client_id         = optional(string)       # Entra, Purview, Teams: the app registration's id; Unity Catalog: its service principal's
    catalog           = optional(string)       # dataplex, unity, glue, purview, or datahub
    location          = optional(string)       # Dataplex: where lineage is kept
    catalogs          = optional(list(string)) # Unity Catalog: its catalogs
    region            = optional(string)       # Glue: the AWS region
    role_arn          = optional(string)       # Glue: the role the runner assumes
    databases         = optional(list(string)) # Glue: its databases
    collections       = optional(list(string)) # Purview: its collections
    platforms         = optional(list(string)) # DataHub: its platforms
    credential_ref    = optional(string)
    every_minutes     = optional(number)
    enabled           = optional(bool, true)
  }))
  default = []

  validation {
    condition = alltrue([for c in var.connections : c.kind == "github" || c.credential_ref != null ||
    (c.kind == "catalog" && contains(["dataplex", "glue"], coalesce(c.catalog, "-")))])
    error_message = "Every source but GitHub, Dataplex, and Glue (read as the runner itself) names a credential_ref: a token never goes in this repository."
  }
  validation {
    condition     = length(distinct([for c in var.connections : c.name])) == length(var.connections)
    error_message = "Each of a team's sources needs its own name."
  }
}

# The team, when its folder says who's on it. Deletion protection stays on: removing team.yaml leaves the team in place
# until an org admin turns it off, so nothing a team holds is purged by a file being deleted.
resource "aerugo_team" "this" {
  count = var.settings == null ? 0 : 1

  slug            = var.team
  name            = var.settings.name
  discoverability = var.settings.discoverability
}

locals {
  grants = var.settings == null ? {} : merge([
    for role in ["admin", "curator", "member", "auditor"] : {
      for principal in var.settings["${role}s"] : "${role}/${principal}" => { role = role, principal = principal }
    }
  ]...)
}

# Each role holder. A group's members are whoever the identity provider puts in it: joining or leaving the group is the
# onboarding, and Aerugo learns it from the person's next sign-in token.
resource "aerugo_team_member" "this" {
  for_each = local.grants

  team      = var.team
  principal = each.value.principal
  role      = each.value.role

  depends_on = [aerugo_team.this]
}

resource "aerugo_connection" "this" {
  for_each   = { for c in var.connections : c.name => c }
  depends_on = [aerugo_team.this]

  team              = var.team
  name              = each.value.name
  kind              = each.value.kind
  site              = each.value.site
  projects          = each.value.projects
  spaces            = each.value.spaces
  labels            = each.value.labels
  page_trees        = each.value.page_trees
  company_site      = each.value.company_site
  installation_id   = each.value.installation_id
  repositories      = each.value.repositories
  channels          = each.value.channels
  sites             = each.value.sites
  team_ids          = each.value.team_ids
  bot_service       = each.value.bot_service
  company_workspace = each.value.company_workspace
  history_days      = each.value.history_days
  drives            = each.value.drives
  domain            = each.value.domain
  directory         = each.value.directory
  admin_email       = each.value.admin_email
  tenant_id         = each.value.tenant_id
  client_id         = each.value.client_id
  catalog           = each.value.catalog
  location          = each.value.location
  catalogs          = each.value.catalogs
  region            = each.value.region
  role_arn          = each.value.role_arn
  databases         = each.value.databases
  collections       = each.value.collections
  platforms         = each.value.platforms
  credential_ref    = each.value.credential_ref
  every_minutes     = each.value.every_minutes
  enabled           = each.value.enabled
}

output "connections" {
  description = "Each source's id (team/name) and status."
  value       = { for name, c in aerugo_connection.this : name => { id = c.id, status = c.status } }
}
