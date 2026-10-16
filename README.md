# Terraform provider for Aerugo

Manage an Aerugo org as code: its teams and members, the models each team asks with, the sources that bring knowledge in, and the rules that keep it. The same provider works against a self-hosted Aerugo and the SaaS; only the endpoint and the service account change. Every change goes through Aerugo's public API, so it passes the same permission checks, validation, and audit as a change made in the app, and the app marks what Terraform manages.

The design is ADR 0009 in the platform's repository.

## What's here

| | |
|---|---|
| `aerugo_team` | A team. Protected from destroy until `deletion_protection = false`; `wait_for_purge` waits for its removal to finish. |
| `aerugo_team_member` | A role on a team for a person, a group, or a service account. Imports as `team/principal/role`. |
| `aerugo_model` | A model a team can ask with, and the classifications it may receive. Its `api_key` is write-only, never in state; change `api_key_version` to rotate it. Imports as `team/name`. |
| `aerugo_model_budget` | A monthly token cap on a team's model, set by org admins. Destroying it removes the cap. Imports as `team/model`. |
| `aerugo_service_account` | A machine identity (an OAuth client from your identity provider) that may call Aerugo's API, with only the roles it's given. It won't disable or remove the account Terraform itself signs in as. |
| `aerugo_connection` | A source a team connects (GitHub, Google Drive, Jira, Confluence, Slack with its `channels`, `company_workspace`, and `history_days`, Microsoft 365 with its `tenant_id`, `client_id`, `sites`, and a write-only `certificate`, Notion (readable by the team's members only), or a directory: Okta, Google Workspace with its `admin_email`, or Entra with its `tenant_id`, `client_id`, and a write-only `certificate`), read by a service account on a schedule, each item keeping its source's own readers. Its credential is a `credential_ref` to a secret the team keeps, or a write-only `api_token` / `service_account_key` (never in state; change `credential_version` to rotate). Cadence, scope lists (projects, spaces, labels, page trees, repositories, drives), and pausing change in place. Imports as `team/name`. |
| `data.aerugo_team` | A team that exists already, with the tool that manages it. |
| `data.aerugo_status` | The API's version and whether each part answers. |

Next: routes, pipelines, imports, secret stores, and compliance rules. To onboard every team's sources from one
reviewed repository (a YAML file per team, plan on each pull request, apply on merge), start from
[examples/onboarding](examples/onboarding).

## Settings

```hcl
provider "aerugo" {
  endpoint          = "https://acme.aerugo.cloud"                                   # or AERUGO_ENDPOINT
  token_url         = "https://login.acme.com/realms/acme/protocol/openid-connect/token" # or AERUGO_TOKEN_URL
  client_id         = "terraform"                                                   # or AERUGO_CLIENT_ID
  # client_secret: set AERUGO_CLIENT_SECRET, so it's never written in a configuration
  managed_workspace = "prod"                                                        # optional; shown in the app
}
```

The service account is an OAuth client in your identity provider, registered in Aerugo by an org admin, with only the roles it needs.

## Try it against your local Aerugo (Windows)

You need Go 1.22 or later, and Terraform 1.11 or later: a model's key is a write-only argument, which older versions don't have.

1. **Build and install the provider** (into `%USERPROFILE%\go\bin`):

   ```powershell
   go mod tidy
   go install .
   ```

2. **Point Terraform at your build** instead of the registry. Create `%APPDATA%\terraform.rc` with:

   ```hcl
   provider_installation {
     dev_overrides {
       "aerugo-hq/aerugo" = "C:/Users/<you>/go/bin"
     }
     direct {}
   }
   ```

   In Git Bash, this writes it for you, with your own Go folder:

   ```bash
   printf 'provider_installation {\n  dev_overrides {\n    "aerugo-hq/aerugo" = "%s/bin"\n  }\n  direct {}\n}\n' "$(go env GOPATH | tr '\\' '/')" > "$APPDATA/terraform.rc"
   ```

3. **Start Aerugo** (`make up` in the platform's repository), then register the development service account. The script asks for an org admin's password (olivia's, in the development realm) and prints the four `$env:AERUGO_*` lines to paste:

   ```powershell
   .\scripts\dev-service-account.ps1
   ```

   The platform's smoke checks remove this registration when they finish, so run the script again after `make smoke`.

4. **Apply the example**, then open the app: the teams are there, marked as managed by Terraform. With `dev_overrides`, skip `terraform init`. The model's key comes from an environment variable, so it's never in a file or in state; the development stack's stand-in model accepts any key.

   ```powershell
   cd examples\complete
   $env:TF_VAR_model_key = "any-key-works-in-development"
   terraform plan
   terraform apply
   ```

   In Git Bash, settings are exported, and paths use forward slashes:

   ```bash
   export AERUGO_ENDPOINT="http://localhost:8000"
   export AERUGO_TOKEN_URL="http://localhost:8081/realms/acme/protocol/openid-connect/token"
   export AERUGO_CLIENT_ID="kl-terraform"
   export AERUGO_CLIENT_SECRET="kl-terraform-dev-secret"   # the development realm's, never a real one
   export TF_VAR_model_key="any-key-works-in-development"
   cd examples/complete
   terraform plan
   terraform apply
   ```

   Change a team's name in the app, run `terraform plan` again, and it shows the change it would undo. `terraform destroy` removes it all.

## Tests

- **Unit tests** need nothing running: `go test ./...` (CI runs these on every push).
- **Acceptance tests** run the real provider against your local Aerugo, with the `AERUGO_*` settings from step 3. They create, import, update, and destroy real teams, and check that a change made in the app shows up as drift:

  ```powershell
  $env:TF_ACC = "1"
  go test -v -count=1 -timeout 30m ./internal/provider
  ```

## Releasing

Pushing a `v*` tag (`git tag v0.1.0; git push origin v0.1.0`) builds every platform, signs the checksums with the release GPG key, and opens a draft GitHub release for review. It needs two repository secrets, `GPG_PRIVATE_KEY` and `PASSPHRASE`, and the key's public half added to the registry. The repository must be public, with a license, before its first registry release.
