package unifi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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

type protectCamera struct {
	ID                string `json:"id"`
	MAC               string `json:"mac"`
	Name              string `json:"name"`
	Host              string `json:"host"`
	Adopted           bool   `json:"isAdopted"`
	AdoptedByOther    bool   `json:"isAdoptedByOther"`
	CanAdopt          bool   `json:"canAdopt"`
	RecordingDisabled bool   `json:"-"`
	RecordingSettings struct {
		Mode string `json:"mode"`
	} `json:"recordingSettings"`
}

func normalizeProtectMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", ""))
}

func (c *consoleClient) camera(ctx context.Context, mac string) (*protectCamera, error) {
	var data struct {
		Cameras []protectCamera `json:"cameras"`
		NVR     struct {
			RecordingDisabled bool `json:"isRecordingDisabled"`
		} `json:"nvr"`
	}
	if err := c.request(ctx, http.MethodGet, "/proxy/protect/api/bootstrap", nil, &data); err != nil {
		return nil, err
	}
	for _, camera := range data.Cameras {
		if normalizeProtectMAC(camera.MAC) == normalizeProtectMAC(mac) {
			camera.RecordingDisabled = data.NVR.RecordingDisabled
			return &camera, nil
		}
	}
	return nil, nil
}

func (c *consoleClient) ensureCamera(ctx context.Context, mac, name, mode, bootstrapHost string) (*protectCamera, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	camera, err := c.camera(ctx, mac)
	if err != nil {
		return nil, err
	}
	if camera == nil && bootstrapHost != "" {
		if err := c.bootstrapCamera(ctx, bootstrapHost, mac); err != nil {
			return nil, err
		}
	}
	err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
		var err error
		camera, err = c.camera(ctx, mac)
		return camera != nil, err
	})
	if err != nil {
		return nil, fmt.Errorf("discover Protect camera %s: %w", mac, err)
	}
	if camera.AdoptedByOther {
		return nil, fmt.Errorf("camera %s belongs to another Protect console; refusing to take ownership", mac)
	}
	if mode != "never" && camera.RecordingDisabled {
		return nil, fmt.Errorf("Protect has recording disabled; configure recording storage and enable recording, or set recording_mode to never for live view only")
	}
	if !camera.Adopted {
		if !camera.CanAdopt {
			return nil, fmt.Errorf("camera %s is not adoptable; check camera and console setup in Protect", mac)
		}
		var result struct {
			Cameras map[string]struct {
				Adopted bool `json:"adopted"`
			} `json:"cameras"`
		}
		err := c.request(ctx, http.MethodPost, "/proxy/protect/api/devices/adopt", map[string]any{"cameras": map[string]any{camera.ID: map[string]any{}}}, &result)
		if err != nil {
			return nil, err
		}
		if !result.Cameras[camera.ID].Adopted {
			return nil, fmt.Errorf("Protect did not accept adoption of camera %s", mac)
		}
		err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
			var err error
			camera, err = c.camera(ctx, mac)
			if camera != nil && camera.AdoptedByOther {
				return false, fmt.Errorf("camera %s was adopted by another console", mac)
			}
			return camera != nil && camera.Adopted, err
		})
		if err != nil {
			return nil, err
		}
	}
	if camera.Name != name || camera.RecordingSettings.Mode != mode {
		err := c.request(ctx, http.MethodPatch, "/proxy/protect/api/cameras/"+url.PathEscape(camera.ID),
			map[string]any{"name": name, "recordingSettings": map[string]any{"mode": mode}}, nil)
		if err != nil {
			return nil, err
		}
	}
	err = waitConsole(ctx, 2*time.Second, func() (bool, error) {
		var err error
		camera, err = c.camera(ctx, mac)
		return camera != nil && camera.Adopted && !camera.AdoptedByOther && camera.Name == name && camera.RecordingSettings.Mode == mode, err
	})
	return camera, err
}

type protectCameraResource struct{ client *consoleClient }
type protectCameraModel struct {
	ID               types.String `tfsdk:"id"`
	MAC              types.String `tfsdk:"mac"`
	Name             types.String `tfsdk:"name"`
	RecordingMode    types.String `tfsdk:"recording_mode"`
	Host             types.String `tfsdk:"host"`
	BootstrapHost    types.String `tfsdk:"bootstrap_host"`
	UnadoptOnDestroy types.Bool   `tfsdk:"unadopt_on_destroy"`
}

func NewProtectCameraResource() resource.Resource { return &protectCameraResource{} }
func (r *protectCameraResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_protect_camera"
}
func (r *protectCameraResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adopts a camera into UniFi Protect and manages its name and recording mode. Requires Protect installed and running, and local OS admin authentication. Never takes over cameras belonging to another console. Cameras remain adopted on destroy by default.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"mac":                schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`), "must be a colon-separated MAC address")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name":               schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"recording_mode":     schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("always", "detections", "never")}},
			"host":               schema.StringAttribute{Computed: true},
			"bootstrap_host":     schema.StringAttribute{Optional: true, MarkdownDescription: "Initial camera IP for configuring its Protect server if discovery has not found it. Uses factory credentials or UNIFI_CAMERA_USERNAME and UNIFI_CAMERA_PASSWORD. Camera credentials are never retained in state."},
			"unadopt_on_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), MarkdownDescription: "Unadopt the camera on destroy; defaults to false to preserve surveillance."},
		},
	}
}
func (r *protectCameraResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *protectCameraResource) apply(ctx context.Context, model *protectCameraModel, diags *diag.Diagnostics) {
	camera, err := r.client.ensureCamera(ctx, model.MAC.ValueString(), model.Name.ValueString(), model.RecordingMode.ValueString(), model.BootstrapHost.ValueString())
	if err != nil {
		diags.AddError("Error Managing Protect Camera", err.Error())
		return
	}
	model.ID = types.StringValue(camera.ID)
	model.Host = types.StringValue(camera.Host)
}
func (r *protectCameraResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model protectCameraModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &model, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}
func (r *protectCameraResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model protectCameraModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &model, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}
func (r *protectCameraResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model protectCameraModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	camera, err := r.client.camera(ctx, model.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Protect Camera", err.Error())
		return
	}
	if camera != nil && camera.AdoptedByOther {
		resp.Diagnostics.AddError("Camera Owned by Another Console", "Refusing to manage this camera. Restore its ownership in Protect before applying.")
		return
	}
	if camera == nil || !camera.Adopted {
		resp.State.RemoveResource(ctx)
		return
	}
	model.ID = types.StringValue(camera.ID)
	model.Name = types.StringValue(camera.Name)
	model.RecordingMode = types.StringValue(camera.RecordingSettings.Mode)
	model.Host = types.StringValue(camera.Host)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
func (r *protectCameraResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model protectCameraModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() || !model.UnadoptOnDestroy.ValueBool() {
		return
	}
	camera, err := r.client.camera(ctx, model.MAC.ValueString())
	if err == nil && camera != nil && camera.Adopted && !camera.AdoptedByOther {
		err = r.client.request(ctx, http.MethodDelete, "/proxy/protect/api/cameras/"+url.PathEscape(camera.ID), nil, nil)
		if err == nil {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
				current, err := r.client.camera(ctx, model.MAC.ValueString())
				return current == nil || !current.Adopted, err
			})
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Unadopting Protect Camera", err.Error())
	}
}
func (r *protectCameraResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("mac"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("unadopt_on_destroy"), false)...)
}
