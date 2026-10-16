# Onboarding sources as code

One repository for every team's sources. Each team keeps one file, `teams/<team>/sources.yaml`, and owns it
(`CODEOWNERS`). A change is a pull request: CI shows the plan on it, and applies it on merge. The platform team owns
the module, the root configuration, and the workflow.

Nothing secret lives here. Each source names a `credential_ref`, a secret the team keeps in the secrets backend
(under `openbao://secret/kl/<org>/<team>/connections/`), so rotating a token never touches this repository. GitHub
reads through the GitHub App, so it needs none.

## Start a repository from this

1. Copy this folder into a new repository.
2. Set `backend "gcs"` in `main.tf` to your state bucket, and give CI's workload identity access to it.
3. Point `terraformrc.example` at your private provider mirror.
4. As an org admin, register the onboarding service account under Developer, and give it a team admin role on the
   teams it manages. Set the repository's variables and secret (listed in the workflow).
5. Turn on branch protection for `main` with "Require review from Code Owners".

## Add a source

Add an entry to your team's file, and open a pull request:

```yaml
connections:
  - name: payments-jira
    kind: jira # github, google, jira, or directory
    site: https://acme.atlassian.net
    projects: [EDP]
    credential_ref: openbao://secret/kl/acme/payments/connections/atlassian-bot
    every_minutes: 15
```

Each entry becomes an `aerugo_connection`: what its fields mean is in the provider's README. In the app, every source
this repository manages shows as "Managed by Terraform, onboarding", so a change made by hand is drift, and the next
plan shows it.
