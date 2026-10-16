package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

var (
	_ resource.Resource                = &connectionResource{}
	_ resource.ResourceWithConfigure   = &connectionResource{}
	_ resource.ResourceWithImportState = &connectionResource{}
)

// The kinds of source a connection can be (ADR 0011), each created through its own API route.
var connectionKinds = []string{"github", "google", "jira", "confluence", "directory"}

type connectionResource struct {
	client *client.Client
}

// The schema calls a directory's provider "directory": provider is one of Terraform's own words in a resource.
type connectionModel struct {
	ID                types.String `tfsdk:"id"`
	Team              types.String `tfsdk:"team"`
	Name              types.String `tfsdk:"name"`
	Kind              types.String `tfsdk:"kind"`
	EveryMinutes      types.Int64  `tfsdk:"every_minutes"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	CredentialRef     types.String `tfsdk:"credential_ref"`
	Site              types.String `tfsdk:"site"`
	Projects          types.List   `tfsdk:"projects"`
	Spaces            types.List   `tfsdk:"spaces"`
	Labels            types.List   `tfsdk:"labels"`
	PageTrees         types.List   `tfsdk:"page_trees"`
	CompanySite       types.Bool   `tfsdk:"company_site"`
	InstallationID    types.Int64  `tfsdk:"installation_id"`
	Repositories      types.List   `tfsdk:"repositories"`
	Drives            types.List   `tfsdk:"drives"`
	Domain            types.String `tfsdk:"domain"`
	Directory         types.String `tfsdk:"directory"`
	Email             types.String `tfsdk:"email"`
	APIToken          types.String `tfsdk:"api_token"`
	ServiceAccountKey types.String `tfsdk:"service_account_key"`
	CredentialVersion types.Int64  `tfsdk:"credential_version"`
	Status            types.String `tfsdk:"status"`
}

// connectionAPI is a connection's read: what it was declared with (its settings), and never a secret. A credential
// reference comes back only when it's one the team keeps itself.
type connectionAPI struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Enabled        bool   `json:"enabled"`
	InstallationID *int64 `json:"installation_id"`
	Settings       struct {
		APIBase       *string  `json:"api_base"`
		Projects      []string `json:"projects"`
		Spaces        []string `json:"spaces"`
		Labels        []string `json:"labels"`
		PageTrees     []string `json:"page_trees"`
		CompanySite   *bool    `json:"company_site"`
		EveryMinutes  *int64   `json:"every_minutes"`
		Domain        *string  `json:"domain"`
		Drives        []string `json:"drives"`
		Repositories  []string `json:"repositories"`
		Provider      *string  `json:"provider"`
		CredentialRef *string  `json:"credential_ref"`
	} `json:"settings"`
}

func connectionPath(team, name string) string {
	return "/v1/workspaces/" + url.PathEscape(team) + "/connections/" + url.PathEscape(name)
}

// fetchConnection reads a team's connection: nil if it, or the team, is gone or out of reach.
func fetchConnection(ctx context.Context, c *client.Client, team, name string) (*connectionAPI, error) {
	var got connectionAPI
	err := c.Do(ctx, http.MethodGet, connectionPath(team, name), nil, nil, &got)
	if client.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &got, nil
}

// NewConnectionResource is aerugo_connection.
func NewConnectionResource() resource.Resource { return &connectionResource{} }

func (r *connectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection"
}

func (r *connectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	removing := listplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.ListRequest, resp *listplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !req.StateValue.IsNull() && req.PlanValue.IsNull()
		},
		"Removing the list connects the source again, to everything its account can reach: the API can change a list, but not remove it.",
		"Removing the list connects the source again, to everything its account can reach: the API can change a list, but not remove it.",
	)
	scope := func(description string) schema.ListAttribute {
		return schema.ListAttribute{
			Optional:      true,
			ElementType:   types.StringType,
			Description:   description,
			PlanModifiers: []planmodifier.List{removing},
			Validators: []validator.List{
				listvalidator.SizeAtMost(500),
				listvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 200)),
			},
		}
	}
	resp.Schema = schema.Schema{
		Description: "A source a team connects (ADR 0011): a service account reads it on a schedule, and each item keeps " +
			"its source's own readers. Its credential is a reference to a secret the team keeps (credential_ref), or a " +
			"write-only argument Terraform sends and never stores in state (api_token, service_account_key), which needs " +
			"Terraform 1.11 or later.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "team/name, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"team": schema.StringAttribute{
				Required: true, Description: "The team's slug: the connection's documents arrive in its space.", PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)},
			},
			"name": schema.StringAttribute{
				Required: true, Description: "The connection's name in its team (" + slugRule + ").", PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)},
			},
			"kind": schema.StringAttribute{
				Required:      true,
				Description:   "What it connects: github, google (Drive), jira, confluence, or directory (who's at the company, and who has left).",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.OneOf(connectionKinds...)},
			},
			"every_minutes": schema.Int64Attribute{
				Optional: true, Computed: true,
				Description:   "How often it syncs, in minutes (5 to 10080); unset, the deployment's default.",
				Validators:    []validator.Int64{int64validator.Between(5, 10080)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "false pauses its syncing; true resumes it.",
			},
			"credential_ref": schema.StringAttribute{
				Optional:      true,
				Description:   "A secret the team keeps: under openbao://secret/kl/<org>/<team>/connections/, or a GCP Secret Manager secret, gcpsm://projects/<project>/secrets/kl_<org>_<team>_<name>. Rotate it where it lives.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 300)},
			},
			"site": schema.StringAttribute{
				Optional:      true,
				Description:   "Jira or Confluence: its site (https://<site>.atlassian.net). Directory: its Okta org (https://<org>.okta.com).",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.LengthBetween(8, 300)},
			},
			"projects":   scope("Jira: the projects it brings in, by key; unset, every project its account can browse."),
			"spaces":     scope("Confluence: the spaces it brings in, by key; unset, every space its account can view."),
			"labels":     scope("Confluence: only pages with one of these labels; unset, every page."),
			"page_trees": scope("Confluence: only these pages, by id, and everything under them; unset, every page."),
			"company_site": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description:   "Jira or Confluence: this site is the company's own, so a project or space anyone there may see reaches the whole org (restrictions still narrow it).",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"installation_id": schema.Int64Attribute{
				Optional:      true,
				Description:   "GitHub: the GitHub App installation it reads through.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
			},
			"repositories": scope("GitHub: the repositories it brings in; unset, every one the installation grants."),
			"drives":       scope("Google Drive: the shared drives it brings in; folders shared with its account always come."),
			"domain": schema.StringAttribute{
				Optional:      true,
				Description:   "Google Drive: the company's domain; a file shared with everyone there reaches the whole org.",
				PlanModifiers: replace,
			},
			"directory": schema.StringAttribute{
				Optional: true, Computed: true,
				Description:   "Directory: which one (okta, its default). Unset for any other kind.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
				Validators:    []validator.String{stringvalidator.OneOf("okta")},
			},
			"email": schema.StringAttribute{
				Optional:    true,
				Description: "Jira or Confluence: the account's email, sent with api_token.",
				Validators:  []validator.String{stringvalidator.LengthBetween(3, 254)},
			},
			"api_token": schema.StringAttribute{
				Optional: true, Sensitive: true, WriteOnly: true,
				Description: "Jira, Confluence, or Okta: the account's API token, when there's no credential_ref. Write-only: pass it from an ephemeral, sensitive variable.",
				Validators:  []validator.String{stringvalidator.LengthBetween(8, 4000)},
			},
			"service_account_key": schema.StringAttribute{
				Optional: true, Sensitive: true, WriteOnly: true,
				Description: "Google Drive: the service account's JSON key, when there's no credential_ref. Write-only, like api_token.",
				Validators:  []validator.String{stringvalidator.LengthBetween(2, 20000)},
			},
			"credential_version": schema.Int64Attribute{
				Optional:    true,
				Description: "Change this to send api_token or service_account_key again: Aerugo replaces the connection's secret.",
			},
			"status": schema.StringAttribute{
				Computed:      true,
				Description:   "pending, ready, error, or disabled.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *connectionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

// connWriteOnly is a write-only argument, which only the configuration holds: never the plan, never the state.
func connWriteOnly(ctx context.Context, cfg interface {
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, name string, diags *diag.Diagnostics) string {
	var v types.String
	diags.Append(cfg.GetAttribute(ctx, path.Root(name), &v)...)
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}

// connStrings is a list for the API, or nil when it isn't set (so the API keeps its own default).
func connStrings(ctx context.Context, list types.List, diags *diag.Diagnostics) []string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	out := []string{}
	diags.Append(list.ElementsAs(ctx, &out, false)...)
	return out
}

func connString(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

func connInt64(n *int64) types.Int64 {
	if n == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*n)
}

func connList(ctx context.Context, items []string, diags *diag.Diagnostics) types.List {
	if items == nil {
		return types.ListNull(types.StringType)
	}
	list, d := types.ListValueFrom(ctx, types.StringType, items)
	diags.Append(d...)
	return list
}

func connSet(v types.String) bool { return !v.IsNull() && !v.IsUnknown() }

// refresh copies what the API holds into Terraform state. Secrets never come back, and the Jira account's email,
// which lives with its token, keeps what the configuration says.
func (r *connectionResource) refresh(ctx context.Context, m *connectionModel, got *connectionAPI, diags *diag.Diagnostics) {
	s := got.Settings
	m.ID = types.StringValue(m.Team.ValueString() + "/" + got.Name)
	m.Name = types.StringValue(got.Name)
	m.Kind = types.StringValue(got.Kind)
	m.Status = types.StringValue(got.Status)
	m.Enabled = types.BoolValue(got.Enabled)
	m.EveryMinutes = connInt64(s.EveryMinutes)
	m.CredentialRef = connString(s.CredentialRef)
	m.CompanySite = types.BoolValue((got.Kind == "jira" || got.Kind == "confluence") && s.CompanySite != nil && *s.CompanySite)
	switch got.Kind {
	case "github":
		m.InstallationID = connInt64(got.InstallationID)
		m.Repositories = connList(ctx, s.Repositories, diags)
	case "google":
		m.Domain = connString(s.Domain)
		m.Drives = connList(ctx, s.Drives, diags)
	case "jira":
		m.Site = connString(s.APIBase)
		m.Projects = connList(ctx, s.Projects, diags)
	case "confluence":
		m.Site = connString(s.APIBase)
		m.Spaces = connList(ctx, s.Spaces, diags)
		m.Labels = connList(ctx, s.Labels, diags)
		m.PageTrees = connList(ctx, s.PageTrees, diags)
	case "directory":
		m.Site = connString(s.APIBase)
	}
	m.Directory = types.StringNull() // computed: known after every read, so a plan never shows it changing
	if got.Kind == "directory" {
		m.Directory = connString(s.Provider)
	}
	m.APIToken = types.StringNull()
	m.ServiceAccountKey = types.StringNull()
}

func (r *connectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	token := connWriteOnly(ctx, req.Config, "api_token", &resp.Diagnostics)
	key := connWriteOnly(ctx, req.Config, "service_account_key", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	team, name, kind := plan.Team.ValueString(), plan.Name.ValueString(), plan.Kind.ValueString()
	route := "/v1/workspaces/" + url.PathEscape(team) + "/connections"
	in := map[string]any{"name": name}
	if !plan.EveryMinutes.IsNull() && !plan.EveryMinutes.IsUnknown() {
		in["every_minutes"] = plan.EveryMinutes.ValueInt64()
	}
	ref := ""
	if connSet(plan.CredentialRef) {
		ref = plan.CredentialRef.ValueString()
	}
	needSite := func() bool {
		if !connSet(plan.Site) {
			resp.Diagnostics.AddAttributeError(path.Root("site"), "Missing site", "A "+kind+" connection names its site.")
			return false
		}
		in["api_base"] = plan.Site.ValueString()
		return true
	}
	oneCredential := func(what string) {
		resp.Diagnostics.AddError("One credential, please", "A "+kind+" connection takes credential_ref (a secret the team keeps), or "+
			what+" (write-only): one, not both.")
	}
	switch kind {
	case "github":
		if plan.InstallationID.IsNull() || plan.InstallationID.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("installation_id"), "Missing installation_id",
				"A GitHub connection reads through a GitHub App installation: name it.")
			return
		}
		in["installation_id"] = plan.InstallationID.ValueInt64()
		if repos := connStrings(ctx, plan.Repositories, &resp.Diagnostics); repos != nil {
			in["repositories"] = repos
		}
	case "google":
		route += "/google"
		switch {
		case ref != "" && key == "":
			in["credential_ref"] = ref
		case ref == "" && key != "":
			in["service_account_key"] = key
		default:
			oneCredential("service_account_key")
			return
		}
		if connSet(plan.Domain) {
			in["domain"] = plan.Domain.ValueString()
		}
		if connSet(plan.Site) {
			in["api_base"] = plan.Site.ValueString()
		}
		if drives := connStrings(ctx, plan.Drives, &resp.Diagnostics); drives != nil {
			in["drives"] = drives
		}
	case "jira":
		route += "/jira"
		if !needSite() {
			return
		}
		in["company_site"] = plan.CompanySite.ValueBool()
		switch {
		case ref != "" && token == "":
			in["credential_ref"] = ref
		case ref == "" && token != "" && connSet(plan.Email):
			in["email"], in["api_token"] = plan.Email.ValueString(), token
		default:
			oneCredential("email with api_token")
			return
		}
		if projects := connStrings(ctx, plan.Projects, &resp.Diagnostics); projects != nil {
			in["projects"] = projects
		}
	case "confluence":
		route += "/confluence"
		if !needSite() {
			return
		}
		in["company_site"] = plan.CompanySite.ValueBool()
		switch {
		case ref != "" && token == "":
			in["credential_ref"] = ref
		case ref == "" && token != "" && connSet(plan.Email):
			in["email"], in["api_token"] = plan.Email.ValueString(), token
		default:
			oneCredential("email with api_token")
			return
		}
		for field, list := range map[string]types.List{"spaces": plan.Spaces, "labels": plan.Labels, "page_trees": plan.PageTrees} {
			if values := connStrings(ctx, list, &resp.Diagnostics); values != nil {
				in[field] = values
			}
		}
	case "directory":
		route += "/directory"
		if !needSite() {
			return
		}
		in["provider"] = "okta"
		if connSet(plan.Directory) {
			in["provider"] = plan.Directory.ValueString()
		}
		switch {
		case ref != "" && token == "":
			in["credential_ref"] = ref
		case ref == "" && token != "":
			in["api_token"] = token
		default:
			oneCredential("api_token")
			return
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Do(ctx, http.MethodPost, route, nil, in, nil); err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddError("That connection already exists",
				fmt.Sprintf("Team %s already has a connection named %q. To manage it with Terraform, import it: terraform import <address> %s/%s", team, name, team, name))
			return
		}
		resp.Diagnostics.AddError("Couldn't connect the source", err.Error())
		return
	}
	if !plan.Enabled.IsNull() && !plan.Enabled.IsUnknown() && !plan.Enabled.ValueBool() {
		if err := r.client.Do(ctx, http.MethodPatch, connectionPath(team, name), nil, map[string]any{"enabled": false}, nil); err != nil {
			resp.Diagnostics.AddError("Connected the source, but couldn't pause it", err.Error())
			return
		}
	}
	got, err := fetchConnection(ctx, r.client, team, name)
	if err != nil || got == nil {
		resp.Diagnostics.AddError("Connected the source, but couldn't read it back", fmt.Sprint(err))
		return
	}
	r.refresh(ctx, &plan, got, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state connectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := fetchConnection(ctx, r.client, state.Team.ValueString(), state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the connection", err.Error())
		return
	}
	if got == nil {
		resp.State.RemoveResource(ctx) // removed in the app: the next plan offers to connect it again
		return
	}
	r.refresh(ctx, &state, got, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *connectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := map[string]any{}
	if !plan.EveryMinutes.IsNull() && !plan.EveryMinutes.IsUnknown() && !plan.EveryMinutes.Equal(state.EveryMinutes) {
		in["every_minutes"] = plan.EveryMinutes.ValueInt64()
	}
	if !plan.Enabled.Equal(state.Enabled) {
		in["enabled"] = plan.Enabled.ValueBool()
	}
	for field, lists := range map[string][2]types.List{
		"projects": {plan.Projects, state.Projects}, "repositories": {plan.Repositories, state.Repositories}, "drives": {plan.Drives, state.Drives},
		"spaces": {plan.Spaces, state.Spaces}, "labels": {plan.Labels, state.Labels}, "page_trees": {plan.PageTrees, state.PageTrees},
	} {
		if !lists[0].IsNull() && !lists[0].Equal(lists[1]) {
			in[field] = connStrings(ctx, lists[0], &resp.Diagnostics)
		}
	}
	if connSet(plan.Email) && !plan.Email.Equal(state.Email) {
		in["email"] = plan.Email.ValueString()
	}
	if !plan.CredentialVersion.Equal(state.CredentialVersion) { // a rotation: send the secret again, only then
		token := connWriteOnly(ctx, req.Config, "api_token", &resp.Diagnostics)
		key := connWriteOnly(ctx, req.Config, "service_account_key", &resp.Diagnostics)
		switch {
		case key != "":
			in["service_account_key"] = key
		case token != "":
			in["api_token"] = token
		default:
			resp.Diagnostics.AddAttributeError(path.Root("credential_version"), "Nothing to send",
				"credential_version changed, which replaces the connection's secret, but neither api_token nor service_account_key was given.")
			return
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	team, name := plan.Team.ValueString(), plan.Name.ValueString()
	if len(in) > 0 {
		if err := r.client.Do(ctx, http.MethodPatch, connectionPath(team, name), nil, in, nil); err != nil {
			resp.Diagnostics.AddError("Couldn't update the connection", err.Error())
			return
		}
	}
	got, err := fetchConnection(ctx, r.client, team, name)
	if err != nil || got == nil {
		resp.Diagnostics.AddError("Updated the connection, but couldn't read it back", fmt.Sprint(err))
		return
	}
	r.refresh(ctx, &plan, got, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state connectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Do(ctx, http.MethodDelete, connectionPath(state.Team.ValueString(), state.Name.ValueString()), nil, nil, nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Couldn't remove the connection", err.Error())
	}
}

func (r *connectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := splitID(req.ID, 2, "team/name, such as payments/jira", &resp.Diagnostics)
	if parts == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}
