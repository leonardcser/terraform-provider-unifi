package unifi

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type osApplicationResource struct{ client *consoleClient }
type osApplicationModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Running            types.Bool   `tfsdk:"running"`
	Version            types.String `tfsdk:"version"`
	UninstallOnDestroy types.Bool   `tfsdk:"uninstall_on_destroy"`
}

func NewOSApplicationResource() resource.Resource { return &osApplicationResource{} }
func (r *osApplicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_os_application"
}
func (r *osApplicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Installs and controls a UniFi OS application. Installing, starting and stopping need a local admin session (`UNIFI_USERNAME` and `UNIFI_PASSWORD`). Without one, `network` and `protect` are read through their integration APIs with the API key, so plans and no-op applies need no admin login. By default removal from Terraform leaves the application and its data intact.",
		Attributes: map[string]schema.Attribute{
			"id":                   schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":                 schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("protect", "network", "access", "talk", "connect", "innerspace")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"running":              schema.BoolAttribute{Required: true},
			"version":              schema.StringAttribute{Computed: true},
			"uninstall_on_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), MarkdownDescription: "Uninstall the application on destroy. May permanently delete application data; defaults to false."},
		},
	}
}
func (r *osApplicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got %T", req.ProviderData))
		return
	}
	r.client = client.console
}
func (r *osApplicationResource) apply(ctx context.Context, model *osApplicationModel, diags *diag.Diagnostics) {
	name := model.Name.ValueString()
	if !r.client.hasSession() && model.Running.ValueBool() {
		if app, err := r.client.runningApplication(ctx, name); err == nil && app != nil {
			model.ID = types.StringValue(app.Name)
			model.Version = types.StringValue(app.Version)
			return
		}
	}
	app, err := r.client.ensureApplication(ctx, name, model.Running.ValueBool())
	if err != nil {
		diags.AddError("Error Managing UniFi OS Application", err.Error())
		return
	}
	model.ID = types.StringValue(app.Name)
	model.Version = types.StringValue(app.Version)
}
func (r *osApplicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model osApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &model, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}
func (r *osApplicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model osApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &model, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}
func (r *osApplicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model osApplicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.client.hasSession() {
		app, err := r.client.runningApplication(ctx, model.Name.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error Reading UniFi OS Application", err.Error())
			return
		}
		// Without a session a silent application is stopped or missing; either
		// way it is not running, and applying needs the admin login.
		model.Running = types.BoolValue(app != nil)
		if app != nil {
			model.Version = types.StringValue(app.Version)
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
		return
	}
	app, err := r.client.application(ctx, model.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading UniFi OS Application", err.Error())
		return
	}
	if !app.Installed {
		resp.State.RemoveResource(ctx)
		return
	}
	model.Running = types.BoolValue(app.Running)
	model.Version = types.StringValue(app.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
func (r *osApplicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model osApplicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() || !model.UninstallOnDestroy.ValueBool() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	app, err := r.client.application(ctx, model.Name.ValueString())
	if err == nil && app.Installed {
		err = r.client.applicationAction(ctx, app.Name, "uninstall")
		if err == nil {
			err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
				current, err := r.client.application(ctx, app.Name)
				if err != nil {
					return false, err
				}
				if current.InstallState == "uninstall_failed" {
					return false, fmt.Errorf("UniFi OS failed to uninstall %s", app.Name)
				}
				return !current.Installed, nil
			})
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Uninstalling UniFi OS Application", err.Error())
	}
}
func (r *osApplicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("uninstall_on_destroy"), false)...)
}
