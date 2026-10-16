package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

var (
	_ resource.Resource                = &teamResource{}
	_ resource.ResourceWithConfigure   = &teamResource{}
	_ resource.ResourceWithImportState = &teamResource{}
)

// How long destroy waits for a team's removal when wait_for_purge is set. The API's own delay window comes first:
// ten seconds in development, an hour by default.
const (
	purgeWait = 2 * time.Hour
	purgePoll = 10 * time.Second
)

type teamResource struct {
	client *client.Client
}

type teamModel struct {
	ID                 types.String `tfsdk:"id"`
	Slug               types.String `tfsdk:"slug"`
	Name               types.String `tfsdk:"name"`
	Discoverability    types.String `tfsdk:"discoverability"`
	DeletionProtection types.Bool   `tfsdk:"deletion_protection"`
	WaitForPurge       types.Bool   `tfsdk:"wait_for_purge"`
}

type teamAPI struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Discoverability string `json:"discoverability"`
}

// NewTeamResource is aerugo_team.
func NewTeamResource() resource.Resource { return &teamResource{} }

func (r *teamResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team"
}

func (r *teamResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A team: its own documents, models, and sources, and the people who work in it. Removing a team " +
			"removes everything in it, so it's protected from destroy until deletion_protection is set to false.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "The team's slug.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"slug": schema.StringAttribute{
				Required:      true,
				Description:   "The team's short name in addresses and in Terraform (" + slugRule + "). Changing it replaces the team.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "The team's name, as people see it.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 120)},
			},
			"discoverability": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("org"),
				Description: "Who can find the team: org (anyone in the org can search it, still within their own permissions) or private (members only).",
				Validators:  []validator.String{stringvalidator.OneOf("org", "private")},
			},
			"deletion_protection": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "While true, destroy refuses to remove the team. Set it to false and apply before destroying.",
			},
			"wait_for_purge": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Whether destroy waits for the team's removal to finish. Otherwise it schedules the removal and returns; until it runs, the team's admins can cancel it in the app.",
			},
		},
	}
}

func (r *teamResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *teamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan teamModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := teamAPI{Slug: plan.Slug.ValueString(), Name: plan.Name.ValueString(), Discoverability: plan.Discoverability.ValueString()}
	if err := r.client.Do(ctx, http.MethodPost, "/v1/workspaces", nil, in, nil); err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddError("That team already exists",
				fmt.Sprintf("A team with the slug %q already exists. To manage it with Terraform, import it instead: terraform import <this resource's address> %s", in.Slug, in.Slug))
			return
		}
		resp.Diagnostics.AddError("Couldn't create the team", err.Error())
		return
	}
	plan.ID = types.StringValue(in.Slug)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *teamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state teamModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var got teamAPI
	err := r.client.Do(ctx, http.MethodGet, "/v1/workspaces/"+url.PathEscape(state.Slug.ValueString()), nil, nil, &got)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx) // removed in the app, or out of reach: the next plan offers to create it again
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the team", err.Error())
		return
	}
	state.ID = types.StringValue(got.Slug)
	state.Slug = types.StringValue(got.Slug)
	state.Name = types.StringValue(got.Name)
	state.Discoverability = types.StringValue(got.Discoverability)
	if state.DeletionProtection.IsNull() { // after an import, the provider's own settings take their defaults
		state.DeletionProtection = types.BoolValue(true)
	}
	if state.WaitForPurge.IsNull() {
		state.WaitForPurge = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *teamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan teamModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	slug := plan.Slug.ValueString()
	in := map[string]string{"name": plan.Name.ValueString(), "discoverability": plan.Discoverability.ValueString()}
	if err := r.client.Do(ctx, http.MethodPatch, "/v1/workspaces/"+url.PathEscape(slug), nil, in, nil); err != nil {
		resp.Diagnostics.AddError("Couldn't update the team", err.Error())
		return
	}
	plan.ID = types.StringValue(slug)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *teamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state teamModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	slug := state.Slug.ValueString()
	if state.DeletionProtection.IsNull() || state.DeletionProtection.ValueBool() {
		resp.Diagnostics.AddError("Deletion protection is on",
			fmt.Sprintf("Removing team %q deletes everything in it: its documents, models, sources, and access. "+
				"To allow it, set deletion_protection = false, apply, then destroy.", slug))
		return
	}
	var purge struct {
		ID       string `json:"id"`
		RunAfter string `json:"run_after"`
	}
	err := r.client.Do(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(slug)+"/purge", nil,
		map[string]string{"confirm": slug}, &purge)
	if client.IsNotFound(err) {
		return // already gone
	}
	if err != nil { // a legal hold, or a purge already scheduled: the API says which
		resp.Diagnostics.AddError("Couldn't schedule the team's removal", err.Error())
		return
	}
	if !state.WaitForPurge.ValueBool() {
		resp.Diagnostics.AddWarning("The team is scheduled for removal",
			fmt.Sprintf("Team %q will be removed after %s. Until then, its admins can cancel the removal in the app.", slug, purge.RunAfter))
		return
	}
	deadline := time.Now().Add(purgeWait)
	for {
		var st struct {
			Status string `json:"status"`
		}
		if err := r.client.Do(ctx, http.MethodGet, "/v1/purges/"+url.PathEscape(purge.ID), nil, nil, &st); err != nil {
			resp.Diagnostics.AddError("Couldn't follow the team's removal", err.Error())
			return
		}
		switch st.Status {
		case "done":
			return
		case "cancelled":
			resp.Diagnostics.AddError("The team's removal was cancelled",
				fmt.Sprintf("The removal of %q was cancelled, in the app or by a legal hold placed after it was scheduled, "+
					"so the team still exists.", slug))
			return
		}
		if time.Now().After(deadline) {
			resp.Diagnostics.AddError("Timed out waiting for the team's removal",
				fmt.Sprintf("The removal of %q is still %s, and will finish on its own. Run terraform refresh later.", slug, st.Status))
			return
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Stopped waiting for the team's removal", ctx.Err().Error())
			return
		case <-time.After(purgePoll):
		}
	}
}

func (r *teamResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}
