package unifi

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ubiquiti-community/go-unifi/unifi"
	"github.com/ubiquiti-community/terraform-provider-unifi/unifi/util"
)

var (
	_ resource.Resource                = &natRuleResource{}
	_ resource.ResourceWithConfigure   = &natRuleResource{}
	_ resource.ResourceWithImportState = &natRuleResource{}
	_ resource.ResourceWithIdentity    = &natRuleResource{}
)

func NewNatRuleResource() resource.Resource {
	return &natRuleResource{}
}

type natRuleResource struct {
	client *Client
}

type natRuleResourceModel struct {
	ID                    types.String    `tfsdk:"id"`
	Site                  types.String    `tfsdk:"site"`
	Description           types.String    `tfsdk:"description"`
	Type                  types.String    `tfsdk:"type"`
	IPVersion             types.String    `tfsdk:"ip_version"`
	Protocol              types.String    `tfsdk:"protocol"`
	Enabled               types.Bool      `tfsdk:"enabled"`
	Logging               types.Bool      `tfsdk:"logging"`
	Exclude               types.Bool      `tfsdk:"exclude"`
	IsPredefined          types.Bool      `tfsdk:"is_predefined"`
	SettingPreference     types.String    `tfsdk:"setting_preference"`
	IPAddress             types.String    `tfsdk:"ip_address"`
	Port                  types.Int64     `tfsdk:"port"`
	InInterface           types.String    `tfsdk:"in_interface"`
	OutInterface          types.String    `tfsdk:"out_interface"`
	PppoeUseBaseInterface types.Bool      `tfsdk:"pppoe_use_base_interface"`
	RuleIndex             types.Int64     `tfsdk:"rule_index"`
	SourceFilter          *natFilterModel `tfsdk:"source_filter"`
	DestinationFilter     *natFilterModel `tfsdk:"destination_filter"`
}

type natFilterModel struct {
	FilterType       types.String `tfsdk:"filter_type"`
	Address          types.String `tfsdk:"address"`
	Port             types.Int64  `tfsdk:"port"`
	NetworkConfID    types.String `tfsdk:"network_conf_id"`
	FirewallGroupIDs types.Set    `tfsdk:"firewall_group_ids"`
	InvertAddress    types.Bool   `tfsdk:"invert_address"`
	InvertPort       types.Bool   `tfsdk:"invert_port"`
}

func (r *natRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nat_rule"
}

func (r *natRuleResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true},
		},
	}
}

func (r *natRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a custom UniFi NAT rule.",
		Attributes: map[string]schema.Attribute{
			"id":                       schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"site":                     schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()}},
			"description":              schema.StringAttribute{Required: true},
			"type":                     schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("DNAT", "SNAT", "MASQUERADE")}},
			"ip_version":               schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("IPV4"), Validators: []validator.String{stringvalidator.OneOf("IPV4", "IPV6")}},
			"protocol":                 schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("all"), Validators: []validator.String{stringvalidator.OneOf("all", "tcp", "udp", "tcp_udp")}},
			"enabled":                  schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"logging":                  schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
			"exclude":                  schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
			"is_predefined":            schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
			"setting_preference":       schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("manual"), Validators: []validator.String{stringvalidator.OneOf("auto", "manual")}},
			"ip_address":               schema.StringAttribute{Optional: true},
			"port":                     schema.Int64Attribute{Optional: true, Validators: []validator.Int64{int64validator.Between(1, 65535)}},
			"in_interface":             schema.StringAttribute{Optional: true},
			"out_interface":            schema.StringAttribute{Optional: true},
			"pppoe_use_base_interface": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
			"rule_index":               schema.Int64Attribute{Optional: true, Computed: true},
			"source_filter":            filterSchema("Source selector for the NAT rule."),
			"destination_filter":       filterSchema("Destination selector for the NAT rule."),
		},
	}
}

func filterSchema(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: description,
		Optional:    true,
		Attributes: map[string]schema.Attribute{
			"filter_type":        schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("NONE", "ADDRESS_AND_PORT", "FIREWALL_GROUPS", "NETWORK_CONF")}},
			"address":            schema.StringAttribute{Optional: true},
			"port":               schema.Int64Attribute{Optional: true, Validators: []validator.Int64{int64validator.Between(1, 65535)}},
			"network_conf_id":    schema.StringAttribute{Optional: true},
			"firewall_group_ids": schema.SetAttribute{Optional: true, ElementType: types.StringType},
			"invert_address":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
			"invert_port":        schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
		},
	}
}

func (r *natRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *natRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan natRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := plan.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	created, err := r.client.CreateNat(ctx, site, modelToNat(ctx, &plan))
	if err != nil {
		resp.Diagnostics.AddError("Error Creating NAT Rule", err.Error())
		return
	}
	natToModel(ctx, created, &plan, site)
	resp.Diagnostics.Append(resp.Identity.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *natRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state natRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := state.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	nat, err := r.client.GetNat(ctx, site, state.ID.ValueString())
	if err != nil {
		if _, ok := err.(*unifi.NotFoundError); ok {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading NAT Rule", err.Error())
		return
	}
	natToModel(ctx, nat, &state, site)
	resp.Diagnostics.Append(resp.Identity.SetAttribute(ctx, path.Root("id"), state.ID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *natRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan natRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state natRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := plan.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	nat := modelToNat(ctx, &plan)
	nat.ID = state.ID.ValueString()
	updated, err := r.client.UpdateNat(ctx, site, nat)
	if err != nil {
		resp.Diagnostics.AddError("Error Updating NAT Rule", err.Error())
		return
	}
	natToModel(ctx, updated, &plan, site)
	resp.Diagnostics.Append(resp.Identity.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *natRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state natRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := state.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	if err := r.client.DeleteNat(ctx, site, state.ID.ValueString()); err != nil {
		if _, ok := err.(*unifi.NotFoundError); ok {
			return
		}
		resp.Diagnostics.AddError("Error Deleting NAT Rule", err.Error())
	}
}

func (r *natRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, diags := util.ParseImportID(req.ID, 1, 2)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if site := parts["site"]; site != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("site"), site)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts["id"])...)
}

func modelToNat(ctx context.Context, model *natRuleResourceModel) *unifi.Nat {
	return &unifi.Nat{
		Description:           model.Description.ValueString(),
		Type:                  model.Type.ValueString(),
		Version:               model.IPVersion.ValueString(),
		Protocol:              model.Protocol.ValueString(),
		Enabled:               model.Enabled.ValueBool(),
		Logging:               model.Logging.ValueBool(),
		Exclude:               model.Exclude.ValueBool(),
		IsPredefined:          model.IsPredefined.ValueBool(),
		SettingPreference:     model.SettingPreference.ValueString(),
		IPAddress:             model.IPAddress.ValueString(),
		Port:                  int64Pointer(model.Port),
		InInterface:           model.InInterface.ValueString(),
		OutInterface:          model.OutInterface.ValueString(),
		PppoeUseBaseInterface: model.PppoeUseBaseInterface.ValueBool(),
		RuleIndex:             int64Pointer(model.RuleIndex),
		SourceFilter:          modelToSourceFilter(ctx, model.SourceFilter),
		DestinationFilter:     modelToDestinationFilter(ctx, model.DestinationFilter),
	}
}

func int64Pointer(value types.Int64) *int64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := value.ValueInt64()
	return &v
}

func modelToSourceFilter(ctx context.Context, model *natFilterModel) *unifi.NatSourceFilter {
	if model == nil {
		return nil
	}
	return &unifi.NatSourceFilter{Address: model.Address.ValueString(), FilterType: model.FilterType.ValueString(), FirewallGroupIDs: stringSet(ctx, model.FirewallGroupIDs), InvertAddress: model.InvertAddress.ValueBool(), InvertPort: model.InvertPort.ValueBool(), NetworkConfID: model.NetworkConfID.ValueString(), Port: int64Pointer(model.Port)}
}

func modelToDestinationFilter(ctx context.Context, model *natFilterModel) *unifi.NatDestinationFilter {
	if model == nil {
		return nil
	}
	return &unifi.NatDestinationFilter{Address: model.Address.ValueString(), FilterType: model.FilterType.ValueString(), FirewallGroupIDs: stringSet(ctx, model.FirewallGroupIDs), InvertAddress: model.InvertAddress.ValueBool(), InvertPort: model.InvertPort.ValueBool(), NetworkConfID: model.NetworkConfID.ValueString(), Port: int64Pointer(model.Port)}
}

func stringSet(ctx context.Context, set types.Set) []string {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	var values []string
	set.ElementsAs(ctx, &values, false)
	return values
}

func natToModel(ctx context.Context, nat *unifi.Nat, model *natRuleResourceModel, site string) {
	model.ID = types.StringValue(nat.ID)
	model.Site = types.StringValue(site)
	model.Description = types.StringValue(nat.Description)
	model.Type = types.StringValue(nat.Type)
	model.IPVersion = types.StringValue(nat.Version)
	model.Protocol = types.StringValue(nat.Protocol)
	model.Enabled = types.BoolValue(nat.Enabled)
	model.Logging = types.BoolValue(nat.Logging)
	model.Exclude = types.BoolValue(nat.Exclude)
	model.IsPredefined = types.BoolValue(nat.IsPredefined)
	model.SettingPreference = types.StringValue(nat.SettingPreference)
	model.IPAddress = nullableString(nat.IPAddress)
	model.Port = nullableInt64(nat.Port)
	model.InInterface = nullableString(nat.InInterface)
	model.OutInterface = nullableString(nat.OutInterface)
	model.PppoeUseBaseInterface = types.BoolValue(nat.PppoeUseBaseInterface)
	model.RuleIndex = nullableInt64(nat.RuleIndex)
	model.SourceFilter = sourceFilterToModel(ctx, nat.SourceFilter)
	model.DestinationFilter = destinationFilterToModel(ctx, nat.DestinationFilter)
}

func nullableString(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

func nullableInt64(value *int64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*value)
}

func sourceFilterToModel(ctx context.Context, filter *unifi.NatSourceFilter) *natFilterModel {
	if filter == nil {
		return nil
	}
	return filterToModel(ctx, filter.FilterType, filter.Address, filter.Port, filter.NetworkConfID, filter.FirewallGroupIDs, filter.InvertAddress, filter.InvertPort)
}

func destinationFilterToModel(ctx context.Context, filter *unifi.NatDestinationFilter) *natFilterModel {
	if filter == nil {
		return nil
	}
	return filterToModel(ctx, filter.FilterType, filter.Address, filter.Port, filter.NetworkConfID, filter.FirewallGroupIDs, filter.InvertAddress, filter.InvertPort)
}

func filterToModel(ctx context.Context, filterType, address string, port *int64, networkID string, groupIDs []string, invertAddress, invertPort bool) *natFilterModel {
	groups, _ := types.SetValueFrom(ctx, types.StringType, groupIDs)
	if len(groupIDs) == 0 {
		groups = types.SetNull(types.StringType)
	}
	return &natFilterModel{FilterType: types.StringValue(filterType), Address: nullableString(address), Port: nullableInt64(port), NetworkConfID: nullableString(networkID), FirewallGroupIDs: groups, InvertAddress: types.BoolValue(invertAddress), InvertPort: types.BoolValue(invertPort)}
}
