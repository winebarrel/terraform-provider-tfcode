// Package tfcode reads the values a Terraform configuration fixes in its own
// source: locals and resource arguments that can be computed without a state
// file, variables or child modules.
//
// A value that cannot be computed is held as a cty unknown while evaluating,
// which is what makes the result trustworthy: try(), keys(), merge() and
// friends already treat unknowns correctly, so an undetermined value cannot
// turn into a plausible wrong answer. Read removes the unknowns at the end, so
// referring to one fails with Terraform's "Unsupported attribute" error.
package tfcode

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"
)

// Values holds what Read found. Each field is an object.
type Values struct {
	Resource cty.Value
	Locals   cty.Value
}

// Read evaluates the *.tf files directly under dir. It only fails when the
// configuration itself is unusable: missing, malformed, or defining a name
// twice or in terms of itself. An undetermined value is not a failure.
func Read(dir string) (*Values, error) {
	e, err := load(dir)
	if err != nil {
		return nil, err
	}

	v := &Values{}
	if v.Resource, err = e.resourceValues(); err != nil {
		return nil, err
	}
	if v.Locals, err = e.localValues(); err != nil {
		return nil, err
	}
	return v, nil
}

type evaluator struct {
	base      *hcl.EvalContext
	locals    map[string]hcl.Expression
	resources map[string]map[string]*hclsyntax.Body
	memo      map[string]cty.Value
	visiting  map[string]bool
}

func load(dir string) (*evaluator, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	sort.Strings(paths)

	e := &evaluator{
		base:      &hcl.EvalContext{Functions: functions()},
		locals:    map[string]hcl.Expression{},
		resources: map[string]map[string]*hclsyntax.Body{},
		memo:      map[string]cty.Value{},
		visiting:  map[string]bool{},
	}

	var diags hcl.Diagnostics
	found := false
	for _, path := range paths {
		base := filepath.Base(path)
		// Terraform ignores hidden files, which is where editors keep drafts.
		if strings.HasPrefix(base, ".") {
			continue
		}
		// Override files replace parts of other blocks. Reading past them would
		// return values Terraform does not use, so refuse instead.
		if base == "override.tf" || strings.HasSuffix(base, "_override.tf") {
			return nil, fmt.Errorf("override files are not supported: %s", path)
		}
		found = true

		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		f, parseDiags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
		if parseDiags.HasErrors() {
			diags = append(diags, parseDiags...)
			continue
		}
		if err := e.collect(f.Body.(*hclsyntax.Body), path); err != nil {
			return nil, err
		}
	}
	if diags.HasErrors() {
		return nil, diags
	}
	if !found {
		return nil, fmt.Errorf("no .tf files in %s", dir)
	}
	return e, nil
}

func (e *evaluator) collect(body *hclsyntax.Body, path string) error {
	for _, block := range body.Blocks {
		switch block.Type {
		case "locals":
			for name, attr := range block.Body.Attributes {
				if _, dup := e.locals[name]; dup {
					return fmt.Errorf("duplicate local %q in %s", name, path)
				}
				e.locals[name] = attr.Expr
			}
		case "resource":
			if len(block.Labels) != 2 {
				continue
			}
			typ, name := block.Labels[0], block.Labels[1]
			if e.resources[typ] == nil {
				e.resources[typ] = map[string]*hclsyntax.Body{}
			}
			if _, dup := e.resources[typ][name]; dup {
				return fmt.Errorf("duplicate resource %s.%s in %s", typ, name, path)
			}
			e.resources[typ][name] = block.Body
		}
	}
	return nil
}

func (e *evaluator) localValues() (cty.Value, error) {
	vals := map[string]cty.Value{}
	for name := range e.locals {
		v, err := e.local(name)
		if err != nil {
			return cty.NilVal, err
		}
		vals[name] = v
	}
	return exportObject(vals), nil
}

func (e *evaluator) resourceValues() (cty.Value, error) {
	types := map[string]cty.Value{}
	for typ, names := range e.resources {
		vals := map[string]cty.Value{}
		for name := range names {
			v, err := e.resource(typ, name)
			if err != nil {
				return cty.NilVal, err
			}
			vals[name] = v
		}
		types[typ] = exportObject(vals)
	}
	return cty.ObjectVal(types), nil
}

// once memoizes a named value and turns a reference back to a value still
// being computed into an error. Terraform rejects such a configuration too.
func (e *evaluator) once(key string, compute func() (cty.Value, error)) (cty.Value, error) {
	if v, ok := e.memo[key]; ok {
		return v, nil
	}
	if e.visiting[key] {
		return cty.NilVal, fmt.Errorf("circular reference involving %s", key)
	}
	e.visiting[key] = true
	defer delete(e.visiting, key)

	v, err := compute()
	if err != nil {
		return cty.NilVal, err
	}
	e.memo[key] = v
	return v, nil
}

func (e *evaluator) local(name string) (cty.Value, error) {
	return e.once("local."+name, func() (cty.Value, error) {
		return e.eval(e.locals[name], e.base)
	})
}

func (e *evaluator) resource(typ, name string) (cty.Value, error) {
	return e.once(typ+"."+name, func() (cty.Value, error) {
		body := e.resources[typ][name]
		if attr, ok := body.Attributes["count"]; ok {
			return e.countInstances(attr.Expr, body)
		}
		if attr, ok := body.Attributes["for_each"]; ok {
			return e.forEachInstances(attr.Expr, body)
		}
		return e.evalBody(body, e.base, true)
	})
}

func (e *evaluator) countInstances(expr hcl.Expression, body *hclsyntax.Body) (cty.Value, error) {
	v, err := e.eval(expr, e.base)
	if err != nil {
		return cty.NilVal, err
	}
	v, convErr := convert.Convert(v, cty.Number)
	if convErr != nil || !v.IsKnown() || v.IsNull() {
		return cty.DynamicVal, nil
	}
	var n int
	if err := gocty.FromCtyValue(v, &n); err != nil || n < 0 {
		return cty.DynamicVal, nil
	}

	instances := make([]cty.Value, n)
	for i := range instances {
		ctx := e.base.NewChild()
		ctx.Variables = map[string]cty.Value{
			"count": cty.ObjectVal(map[string]cty.Value{"index": cty.NumberIntVal(int64(i))}),
		}
		if instances[i], err = e.evalBody(body, ctx, true); err != nil {
			return cty.NilVal, err
		}
	}
	return cty.TupleVal(instances), nil
}

func (e *evaluator) forEachInstances(expr hcl.Expression, body *hclsyntax.Body) (cty.Value, error) {
	v, err := e.eval(expr, e.base)
	if err != nil {
		return cty.NilVal, err
	}
	ty := v.Type()
	iterable := ty.IsMapType() || ty.IsObjectType() || ty.Equals(cty.Set(cty.String))
	if !iterable || !v.IsKnown() || v.IsNull() || hasIndefiniteElements(v) {
		return cty.DynamicVal, nil
	}

	instances := map[string]cty.Value{}
	for _, el := range elements(v) {
		ctx := e.base.NewChild()
		ctx.Variables = map[string]cty.Value{
			"each": cty.ObjectVal(map[string]cty.Value{"key": el.key, "value": el.value}),
		}
		if instances[el.key.AsString()], err = e.evalBody(body, ctx, true); err != nil {
			return cty.NilVal, err
		}
	}
	return cty.ObjectVal(instances), nil
}

// hasIndefiniteElements reports a set with an unknown element. Its keys are not
// known, and neither is its length, since the unknown may equal another element.
func hasIndefiniteElements(v cty.Value) bool {
	return v.Type().IsSetType() && !v.IsWhollyKnown()
}

type element struct{ key, value cty.Value }

// elements lists a known collection the way for_each sees it: a set has its
// value as the key, and a sequence has its index.
func elements(v cty.Value) []element {
	var out []element
	for it := v.ElementIterator(); it.Next(); {
		key, value := it.Element()
		out = append(out, element{key, value})
	}
	return out
}

// Arguments Terraform itself consumes. They are not values of the resource.
var metaAttributes = map[string]bool{"count": true, "for_each": true, "depends_on": true, "provider": true}
var metaBlocks = map[string]bool{"lifecycle": true, "connection": true, "provisioner": true}

func (e *evaluator) evalBody(body *hclsyntax.Body, ctx *hcl.EvalContext, isResource bool) (cty.Value, error) {
	vals := map[string]cty.Value{}
	for name, attr := range body.Attributes {
		if isResource && metaAttributes[name] {
			continue
		}
		v, err := e.eval(attr.Expr, ctx)
		if err != nil {
			return cty.NilVal, err
		}
		vals[name] = v
	}

	blocks := map[string][]cty.Value{}
	undetermined := map[string]bool{}
	for _, block := range body.Blocks {
		if isResource && metaBlocks[block.Type] {
			continue
		}
		if block.Type == "dynamic" && len(block.Labels) == 1 {
			label := block.Labels[0]
			expanded, known, err := e.expandDynamic(block, ctx)
			if err != nil {
				return cty.NilVal, err
			}
			if !known {
				undetermined[label] = true
			}
			blocks[label] = append(blocks[label], expanded...)
			continue
		}
		v, err := e.evalBody(block.Body, ctx, false)
		if err != nil {
			return cty.NilVal, err
		}
		blocks[block.Type] = append(blocks[block.Type], v)
	}
	for name, list := range blocks {
		vals[name] = cty.TupleVal(list)
	}
	// A list with a block of unknown length has no reliable indexes.
	for name := range undetermined {
		vals[name] = cty.DynamicVal
	}
	return cty.ObjectVal(vals), nil
}

func (e *evaluator) expandDynamic(block *hclsyntax.Block, ctx *hcl.EvalContext) ([]cty.Value, bool, error) {
	forEach, ok := block.Body.Attributes["for_each"]
	if !ok {
		return nil, false, nil
	}
	var content *hclsyntax.Body
	for _, b := range block.Body.Blocks {
		if b.Type == "content" {
			content = b.Body
		}
	}
	if content == nil {
		return nil, false, nil
	}

	iterator := block.Labels[0]
	if attr, ok := block.Body.Attributes["iterator"]; ok {
		if iterator = hcl.ExprAsKeyword(attr.Expr); iterator == "" {
			return nil, false, nil
		}
	}

	v, err := e.eval(forEach.Expr, ctx)
	if err != nil {
		return nil, false, err
	}
	if !v.IsKnown() || v.IsNull() || !v.CanIterateElements() || hasIndefiniteElements(v) {
		return nil, false, nil
	}

	var out []cty.Value
	for _, el := range elements(v) {
		child := ctx.NewChild()
		child.Variables = map[string]cty.Value{
			iterator: cty.ObjectVal(map[string]cty.Value{"key": el.key, "value": el.value}),
		}
		instance, err := e.evalBody(content, child, false)
		if err != nil {
			return nil, false, err
		}
		out = append(out, instance)
	}
	return out, true, nil
}

// eval computes an expression, returning an unknown when it is undetermined.
// Object and tuple constructors are taken apart first so that one undetermined
// element leaves the others readable.
func (e *evaluator) eval(expr hcl.Expression, ctx *hcl.EvalContext) (cty.Value, error) {
	switch x := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return e.eval(x.Expression, ctx)
	case *hclsyntax.TupleConsExpr:
		vals := make([]cty.Value, len(x.Exprs))
		for i, item := range x.Exprs {
			v, err := e.eval(item, ctx)
			if err != nil {
				return cty.NilVal, err
			}
			vals[i] = v
		}
		return cty.TupleVal(vals), nil
	case *hclsyntax.ObjectConsExpr:
		vals := map[string]cty.Value{}
		for _, item := range x.Items {
			key, err := e.eval(item.KeyExpr, ctx)
			if err != nil {
				return cty.NilVal, err
			}
			key, convErr := convert.Convert(key, cty.String)
			if convErr != nil || !key.IsKnown() || key.IsNull() {
				return cty.DynamicVal, nil
			}
			v, err := e.eval(item.ValueExpr, ctx)
			if err != nil {
				return cty.NilVal, err
			}
			vals[key.AsString()] = v
		}
		return cty.ObjectVal(vals), nil
	}

	child, determined, err := e.scope(expr, ctx)
	if err != nil {
		return cty.NilVal, err
	}
	if !determined {
		return cty.DynamicVal, nil
	}
	v, diags := expr.Value(child)
	if diags.HasErrors() {
		return cty.DynamicVal, nil
	}
	return v, nil
}

// scope builds the variables an expression needs. It reports false when the
// expression refers to something with no static value: var, data, module,
// path and the like, a resource argument the configuration does not set, or a
// whole resource, whose provider-computed attributes are not known. Without
// this check try(aws_vpc.main.id, "x") would answer "x", because the attribute
// is simply absent from the object here.
func (e *evaluator) scope(expr hcl.Expression, ctx *hcl.EvalContext) (*hcl.EvalContext, bool, error) {
	locals := map[string]cty.Value{}
	resources := map[string]map[string]cty.Value{}

	for _, tr := range expr.Variables() {
		root := tr.RootName()
		if inScope(ctx, root) {
			continue
		}
		if len(tr) < 2 {
			return nil, false, nil
		}
		attr, ok := tr[1].(hcl.TraverseAttr)
		if !ok {
			return nil, false, nil
		}

		if root == "local" {
			if _, declared := e.locals[attr.Name]; !declared {
				return nil, false, nil
			}
			v, err := e.local(attr.Name)
			if err != nil {
				return nil, false, err
			}
			locals[attr.Name] = v
			continue
		}

		if _, declared := e.resources[root][attr.Name]; !declared {
			return nil, false, nil
		}
		v, err := e.resource(root, attr.Name)
		if err != nil {
			return nil, false, err
		}
		if !reachesSetArgument(v, tr[2:]) {
			return nil, false, nil
		}
		if resources[root] == nil {
			resources[root] = map[string]cty.Value{}
		}
		resources[root][attr.Name] = v
	}

	child := ctx.NewChild()
	child.Variables = map[string]cty.Value{}
	if len(locals) > 0 {
		child.Variables["local"] = cty.ObjectVal(locals)
	}
	for typ, names := range resources {
		child.Variables[typ] = cty.ObjectVal(names)
	}
	return child, true, nil
}

func inScope(ctx *hcl.EvalContext, name string) bool {
	for ; ctx != nil; ctx = ctx.Parent() {
		if _, ok := ctx.Variables[name]; ok {
			return true
		}
	}
	return false
}

// reachesSetArgument walks the steps after <type>.<name> and reports whether
// they end at an argument written in the configuration. A dynamic index leaves
// no steps here, so aws_x.a[local.k].name is treated as undetermined.
func reachesSetArgument(v cty.Value, steps hcl.Traversal) bool {
	if !v.IsKnown() {
		return true
	}
	if len(steps) == 0 {
		return false
	}
	for i, step := range steps {
		next, diags := step.TraversalStep(v)
		if diags.HasErrors() {
			return false
		}
		if !next.IsKnown() {
			return true
		}
		// An instance of a count or for_each resource is still a whole resource.
		if _, indexed := step.(hcl.TraverseIndex); indexed && i == 0 && len(steps) == 1 {
			return false
		}
		v = next
	}
	return true
}

// hideInfinity makes an infinite number unknown. It cannot be written to state,
// so one such value would otherwise fail the whole data source.
func hideInfinity(v cty.Value) cty.Value {
	out, err := cty.Transform(v, func(_ cty.Path, v cty.Value) (cty.Value, error) {
		if v.Type() == cty.Number && v.IsKnown() && !v.IsNull() && v.AsBigFloat().IsInf() {
			return cty.UnknownVal(cty.Number), nil
		}
		return v, nil
	})
	if err != nil {
		return cty.DynamicVal
	}
	return out
}

func exportObject(vals map[string]cty.Value) cty.Value {
	out := map[string]cty.Value{}
	for name, v := range vals {
		if exported, ok := export(hideInfinity(v)); ok {
			out[name] = exported
		}
	}
	return cty.ObjectVal(out)
}

// export removes unknowns, reporting false when nothing readable is left. An
// object just loses the attribute. A sequence with an unknown element is
// dropped whole, because removing the element would shift the later indexes
// onto the wrong values.
func export(v cty.Value) (cty.Value, bool) {
	if v.IsWhollyKnown() {
		return v, true
	}
	if !v.IsKnown() {
		return cty.NilVal, false
	}

	ty := v.Type()
	switch {
	case ty.IsObjectType() || ty.IsMapType():
		return exportObject(v.AsValueMap()), true
	case ty.IsTupleType() || ty.IsListType():
		var out []cty.Value
		for _, el := range v.AsValueSlice() {
			exported, ok := export(el)
			if !ok {
				return cty.NilVal, false
			}
			out = append(out, exported)
		}
		return cty.TupleVal(out), true
	}
	return cty.NilVal, false
}
