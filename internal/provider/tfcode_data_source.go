package provider

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/winebarrel/terraform-provider-tfcode/internal/tfcode"
	"github.com/zclconf/go-cty/cty"
)

var _ datasource.DataSource = &TfcodeDataSource{}

var sourcePath = path.Root("source")

type TfcodeDataSource struct{}

func NewTfcodeDataSource() datasource.DataSource {
	return &TfcodeDataSource{}
}

type tfcodeModel struct {
	Source   types.String  `tfsdk:"source"`
	Resource types.Dynamic `tfsdk:"resource"`
	Locals   types.Dynamic `tfsdk:"locals"`
}

func (d *TfcodeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName
}

func (d *TfcodeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads static values from the Terraform configuration in another directory, without using its state.",
		Attributes: map[string]schema.Attribute{
			"source": schema.StringAttribute{
				MarkdownDescription: "Relative path of the directory to read. It is resolved against the directory Terraform runs in, so inside a module write `\"${path.module}/../foo\"`.",
				Required:            true,
			},
			"resource": schema.DynamicAttribute{
				MarkdownDescription: "The arguments set on `resource` blocks, as `resource.<type>.<name>.<argument>`. Nested blocks are always lists.",
				Computed:            true,
			},
			"locals": schema.DynamicAttribute{
				MarkdownDescription: "The `locals` values, as `locals.<name>`.",
				Computed:            true,
			},
		},
	}
}

func (d *TfcodeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data tfcodeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	source := data.Source.ValueString()
	if filepath.IsAbs(source) {
		resp.Diagnostics.AddAttributeError(sourcePath, "Invalid source", fmt.Sprintf("source must be a relative path: %s", source))
		return
	}

	values, err := tfcode.Read(source)
	if err != nil {
		resp.Diagnostics.AddAttributeError(sourcePath, "Failed to read Terraform configuration", err.Error())
		return
	}

	data.Resource = dynamicOf(ctx, values.Resource, &resp.Diagnostics)
	data.Locals = dynamicOf(ctx, values.Locals, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func dynamicOf(ctx context.Context, v cty.Value, diags *diag.Diagnostics) types.Dynamic {
	val, err := toAttr(ctx, v)
	if err != nil {
		diags.AddError("Failed to convert value", err.Error())
		return types.DynamicNull()
	}
	return types.DynamicValue(val)
}

// toAttr converts a wholly known cty value to its framework equivalent. A null
// or an empty collection with no known type becomes a null string or an empty
// tuple, which Terraform converts to whatever the reference needs.
func toAttr(ctx context.Context, v cty.Value) (attr.Value, error) {
	ty := v.Type()
	if v.IsNull() {
		return nullAttr(ty), nil
	}

	var diags diag.Diagnostics
	var out attr.Value
	switch {
	case ty == cty.String:
		out = types.StringValue(v.AsString())
	case ty == cty.Number:
		out = types.NumberValue(v.AsBigFloat())
	case ty == cty.Bool:
		out = types.BoolValue(v.True())
	case ty.IsObjectType() || ty.IsMapType() && ty.HasDynamicTypes():
		vals, attrTypes, err := attrMap(ctx, v)
		if err != nil {
			return nil, err
		}
		out, diags = types.ObjectValue(attrTypes, vals)
	case ty.IsTupleType() || ty.IsCollectionType() && ty.HasDynamicTypes():
		vals, elemTypes, err := attrSlice(ctx, v)
		if err != nil {
			return nil, err
		}
		out, diags = types.TupleValue(elemTypes, vals)
	case ty.IsMapType():
		vals, _, err := attrMap(ctx, v)
		if err != nil {
			return nil, err
		}
		out, diags = types.MapValue(attrType(ty.ElementType()), vals)
	case ty.IsListType():
		vals, _, err := attrSlice(ctx, v)
		if err != nil {
			return nil, err
		}
		out, diags = types.ListValue(attrType(ty.ElementType()), vals)
	case ty.IsSetType():
		vals, _, err := attrSlice(ctx, v)
		if err != nil {
			return nil, err
		}
		out, diags = types.SetValue(attrType(ty.ElementType()), vals)
	default:
		return nil, fmt.Errorf("unsupported type: %s", ty.FriendlyName())
	}
	if diags.HasError() {
		return nil, fmt.Errorf("%s: %s", diags[0].Summary(), diags[0].Detail())
	}
	return out, nil
}

func attrMap(ctx context.Context, v cty.Value) (map[string]attr.Value, map[string]attr.Type, error) {
	vals := map[string]attr.Value{}
	attrTypes := map[string]attr.Type{}
	for name, el := range v.AsValueMap() {
		val, err := toAttr(ctx, el)
		if err != nil {
			return nil, nil, err
		}
		vals[name] = val
		attrTypes[name] = val.Type(ctx)
	}
	return vals, attrTypes, nil
}

func attrSlice(ctx context.Context, v cty.Value) ([]attr.Value, []attr.Type, error) {
	vals := []attr.Value{}
	elemTypes := []attr.Type{}
	for _, el := range v.AsValueSlice() {
		val, err := toAttr(ctx, el)
		if err != nil {
			return nil, nil, err
		}
		vals = append(vals, val)
		elemTypes = append(elemTypes, val.Type(ctx))
	}
	return vals, elemTypes, nil
}

func attrType(ty cty.Type) attr.Type {
	switch {
	case ty == cty.Number:
		return types.NumberType
	case ty == cty.Bool:
		return types.BoolType
	case ty.IsListType():
		return types.ListType{ElemType: attrType(ty.ElementType())}
	case ty.IsSetType():
		return types.SetType{ElemType: attrType(ty.ElementType())}
	case ty.IsMapType():
		return types.MapType{ElemType: attrType(ty.ElementType())}
	case ty.IsObjectType():
		attrTypes := map[string]attr.Type{}
		for name, t := range ty.AttributeTypes() {
			attrTypes[name] = attrType(t)
		}
		return types.ObjectType{AttrTypes: attrTypes}
	case ty.IsTupleType():
		elemTypes := []attr.Type{}
		for _, t := range ty.TupleElementTypes() {
			elemTypes = append(elemTypes, attrType(t))
		}
		return types.TupleType{ElemTypes: elemTypes}
	}
	return types.StringType
}

func nullAttr(ty cty.Type) attr.Value {
	switch t := attrType(ty).(type) {
	case types.ObjectType:
		return types.ObjectNull(t.AttrTypes)
	case types.TupleType:
		return types.TupleNull(t.ElemTypes)
	case types.ListType:
		return types.ListNull(t.ElemType)
	case types.SetType:
		return types.SetNull(t.ElemType)
	case types.MapType:
		return types.MapNull(t.ElemType)
	}
	switch ty {
	case cty.Number:
		return types.NumberNull()
	case cty.Bool:
		return types.BoolNull()
	}
	return types.StringNull()
}
