# One team's sources, as its teams/<team>/sources.yaml declares them: each becomes an aerugo_connection. The platform
# team owns this module; each team owns its own file (CODEOWNERS), so a team changes its sources by pull request, and
# nobody else's. No secret ever lives here: every source but GitHub (which reads through the GitHub App) names a
# credential_ref, a secret the team keeps in the secrets backend, rotated where it lives.
terraform {
  required_providers {
    aerugo = {
      source = "aerugo-hq/aerugo"
    }
  }
}

variable "team" {
  description = "The team's slug. The team itself comes from your org's configuration (aerugo_team)."
  type        = string
}

variable "connections" {
  description = "The team's sources, from its sources.yaml."
  type = list(object({
    name              = string
    kind              = string           # github, google, jira, confluence, slack, microsoft, notion, or directory
    site              = optional(string) # Jira's or Confluence's site, or a directory's Okta org
    projects          = optional(list(string))
    spaces            = optional(list(string)) # Confluence: its spaces, by key
    labels            = optional(list(string)) # Confluence: only pages with one of these labels
    page_trees        = optional(list(string)) # Confluence: only these pages, by id, and what's under them
    company_site      = optional(bool, false)
    installation_id   = optional(number)
    repositories      = optional(list(string))
    channels          = optional(list(string)) # Slack: its channels, by id
    sites             = optional(list(string)) # Microsoft 365: its granted sites, by Graph's site id
    company_workspace = optional(bool, false)  # Slack: the company's own workspace
    history_days      = optional(number)       # Slack: how far back it reads
    drives            = optional(list(string))
    domain            = optional(string)
    directory         = optional(string) # okta, google (Google Workspace), or entra
    admin_email       = optional(string) # Google Workspace: the read-only admin it acts as
    tenant_id         = optional(string) # Entra, Microsoft 365: the tenant's id
    client_id         = optional(string) # Entra: the app registration's id
    credential_ref    = optional(string)
    every_minutes     = optional(number)
    enabled           = optional(bool, true)
  }))
  default = []

  validation {
    condition     = alltrue([for c in var.connections : c.kind == "github" || c.credential_ref != null])
    error_message = "Every source but GitHub names a credential_ref: a token never goes in this repository."
  }
  validation {
    condition     = length(distinct([for c in var.connections : c.name])) == length(var.connections)
    error_message = "Each of a team's sources needs its own name."
  }
}

resource "aerugo_connection" "this" {
  for_each = { for c in var.connections : c.name => c }

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
  company_workspace = each.value.company_workspace
  history_days      = each.value.history_days
  drives            = each.value.drives
  domain            = each.value.domain
  directory         = each.value.directory
  admin_email       = each.value.admin_email
  tenant_id         = each.value.tenant_id
  client_id         = each.value.client_id
  credential_ref    = each.value.credential_ref
  every_minutes     = each.value.every_minutes
  enabled           = each.value.enabled
}

output "connections" {
  description = "Each source's id (team/name) and status."
  value       = { for name, c in aerugo_connection.this : name => { id = c.id, status = c.status } }
}
