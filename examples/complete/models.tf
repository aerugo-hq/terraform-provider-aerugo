# The support team's model, and the cap on what it may use. The key is a write-only argument: Terraform sends it and
# never stores it in state. Pass it as TF_VAR_model_key (the development stack's stand-in model accepts any key).

variable "model_key" {
  description = "The model's API key. Never stored in state."
  type        = string
  sensitive   = true
  ephemeral   = true
}

resource "aerugo_model" "support_assistant" {
  team                    = aerugo_team.support.slug
  name                    = "assistant"
  api                     = "openai_compatible"
  model                   = "fake"
  base_url                = "http://fake-llm:8080/v1" # the development stack's stand-in model
  api_key                 = var.model_key
  api_key_version         = 1 # change it to send a new key: Aerugo destroys every earlier one
  allowed_classifications = ["public", "internal"]
}

resource "aerugo_model_budget" "support_assistant" {
  team           = aerugo_team.support.slug
  model          = aerugo_model.support_assistant.name
  monthly_tokens = 200000
}

# A CI pipeline's own identity, admin of the support team only.
resource "aerugo_service_account" "support_ci" {
  client_id = "support-ci"
  name      = "Support team's CI"
}

resource "aerugo_team_member" "support_ci" {
  team      = aerugo_team.support.slug
  principal = aerugo_service_account.support_ci.principal
  role      = "admin"
}
