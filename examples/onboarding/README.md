# Onboarding teams and their sources as code

One repository for every team: who is on it and what it reads. Each team has a folder, `teams/<team>/`: its
`team.yaml` says who is on the team, and the platform team approves it (`CODEOWNERS`), since it decides who's
onboarded; its `sources.yaml` lists the team's sources, and the team owns it. `org.yaml` says who gets in. A change
is a pull request: CI shows the plan on it, and applies it on merge. The platform team owns the module, the root
configuration, and the workflow.

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

## Onboard a team, a wave at a time

Start with a pilot of a team or two, say EDW and EDP, and add the rest when they're ready.

1. **Groups in the identity provider.** Aerugo signs people in through Keycloak, which brokers your identity provider
   (Entra, Okta, or Google). Add a group mapper to that broker so each person's groups reach their token as `groups`.
   Entra names groups by object id unless the app registration is set to emit names: either works here.
2. **Start the pilot.** Set `admission: teams` in `org.yaml`. Then only members of the teams in this repository, and org
   admins, get in; anyone else who signs in is told their team isn't onboarded yet, so nothing a source shares
   company-wide reaches them. In Entra, also set Aerugo's enterprise app to "assignment required" and assign only the
   pilot's groups, so nobody else can sign in at all.
3. **Add a team.** A pull request adding its folder: `team.yaml` (approved by the platform team) and `sources.yaml`.

   ```yaml
   # teams/edw/team.yaml
   name: Enterprise data warehouse
   admins: [group:edw-leads]
   members: [group:edw-engineering]
   ```

   Someone joins the team by joining the group in the identity provider: no pull request per person, and Aerugo learns
   it at their next sign-in. Leaving the group takes the team away the same way.
4. **End the pilot.** Set `admission: org` once every team is in.

Removing a team's `team.yaml` leaves the team in place (its deletion protection stays on), so a deleted file never
purges what a team holds.
