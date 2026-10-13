package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

var (
	_ resource.Resource                = &serviceAccountResource{}
	_ resource.ResourceWithConfigure   = &serviceAccountResource{}
	_ resource.ResourceWithImportState = &serviceAccountResource{}
)

var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

type serviceAccountResource struct {
	client *client.Client
}

type serviceAccountModel struct {
	ID        types.String `tfsdk:"id"`
	ClientID  types.String `tfsdk:"client_id"`
	Name      types.String `tfsdk:"name"`
	Enabled   types.Bool   `tfsdk:"enabled"`
	OrgAdmin  types.Bool   `tfsdk:"org_admin"`
	Principal types.String `tfsdk:"principal"`
}

type serviceAccountAPI struct {
	ClientID  string `json:"client_id"`
	Principal string `json:"principal"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	OrgAdmin  bool   `json:"org_admin"`
}

// NewServiceAccountResource is aerugo_service_account.
func NewServiceAccountResource() resource.Resource { return &serviceAccountResource{} }

func (r *serviceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An OAuth client from your identity provider, registered as a service account: a machine identity, " +
			"such as a CI pipeline's. Its own tokens work only while it's registered and enabled, with only the roles " +
			"it's given, and it reads no documents unless granted. Give it team roles with aerugo_team_member and its principal.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The client ID, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"client_id": schema.StringAttribute{
				Required:      true,
				Description:   "The OAuth client's ID in your identity provider. Changing it registers a different account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.RegexMatches(clientIDPattern, "must be a client ID: letters, digits, dots, dashes, underscores")},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "What the account is for, as people see it.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 80)},
			},
			"enabled": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "While false, its tokens are refused on their very next request.",
			},
			"org_admin": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Whether it administers the whole org. Prefer team roles where they're enough.",
			},
			"principal": schema.StringAttribute{
				Computed:      true,
				Description:   "user:service-account-<client_id>: what to give team roles to, in aerugo_team_member.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *serviceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func accountPath(id string) string { return "/v1/service-accounts/" + url.PathEscape(id) }

func (m *serviceAccountModel) from(got serviceAccountAPI) {
	m.ID = types.StringValue(got.ClientID)
	m.ClientID = types.StringValue(got.ClientID)
	m.Name = types.StringValue(got.Name)
	m.Enabled = types.BoolValue(got.Enabled)
	m.OrgAdmin = types.BoolValue(got.OrgAdmin)
	m.Principal = types.StringValue(got.Principal)
}

// lockout is the explanation when a change would cut off the service account this provider signs in as.
const lockout = "That's the service account this Terraform signs in as. Disabling it, removing it, or taking away its " +
	"org admin would lock this configuration out partway through. Change it in the app, or from a configuration " +
	"that signs in as a different account."

func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := plan.ClientID.ValueString()
	in := map[string]any{"client_id": id, "name": plan.Name.ValueString(), "org_admin": plan.OrgAdmin.ValueBool()}
	var got serviceAccountAPI
	if err := r.client.Do(ctx, http.MethodPost, "/v1/service-accounts", nil, in, &got); err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddError("That client is already a service account",
				fmt.Sprintf("%q is registered already. To manage it with Terraform, import it: terraform import <address> %s", id, id))
			return
		}
		resp.Diagnostics.AddError("Couldn't register the service account", err.Error())
		return
	}
	if !plan.Enabled.ValueBool() { // registered accounts start enabled
		if err := r.client.Do(ctx, http.MethodPatch, accountPath(id), nil, map[string]any{"enabled": false}, &got); err != nil {
			resp.Diagnostics.AddError("Registered the service account, but couldn't disable it", err.Error())
			return
		}
	}
	plan.from(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var got serviceAccountAPI
	err := r.client.Do(ctx, http.MethodGet, accountPath(state.ClientID.ValueString()), nil, nil, &got)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx) // removed in the app: the next plan offers to register it again
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the service account", err.Error())
		return
	}
	state.from(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := plan.ClientID.ValueString()
	if id == r.client.ClientID() && (!plan.Enabled.ValueBool() || (state.OrgAdmin.ValueBool() && !plan.OrgAdmin.ValueBool())) {
		resp.Diagnostics.AddError("Not changing the account Terraform signs in as", lockout)
		return
	}
	in := map[string]any{"name": plan.Name.ValueString(), "enabled": plan.Enabled.ValueBool(), "org_admin": plan.OrgAdmin.ValueBool()}
	var got serviceAccountAPI
	if err := r.client.Do(ctx, http.MethodPatch, accountPath(id), nil, in, &got); err != nil {
		resp.Diagnostics.AddError("Couldn't update the service account", err.Error())
		return
	}
	plan.from(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ClientID.ValueString()
	if id == r.client.ClientID() {
		resp.Diagnostics.AddError("Not removing the account Terraform signs in as", lockout)
		return
	}
	if err := r.client.Do(ctx, http.MethodDelete, accountPath(id), nil, nil, nil); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Couldn't remove the service account", err.Error())
	}
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("client_id"), req, resp)
}
