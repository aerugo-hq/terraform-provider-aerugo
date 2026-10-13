package provider

import (
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

// The API's own patterns, checked at plan time so a bad name fails before anything is sent. Each resource's own
// patterns and helpers live in its file (models in model_resource.go, service accounts in service_account_resource.go).
var (
	slugPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)                        // teams, models, pipelines
	principalPattern = regexp.MustCompile(`^(user|group):[A-Za-z0-9][A-Za-z0-9._@-]{0,127}$`) // who a role is given to
)

const slugRule = "lowercase letters, digits, and dashes, starting with a letter or a digit, 40 at most"

// providerClient is the client the provider configured, for a resource's or a data source's Configure.
func providerClient(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil // Terraform calls Configure before the provider is configured, during validation
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", data))
		return nil
	}
	return c
}
