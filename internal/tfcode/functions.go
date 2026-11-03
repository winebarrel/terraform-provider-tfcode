package tfcode

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// functions returns the Terraform functions available to expressions. The
// real ones live in an internal Terraform package, so this is the go-cty
// standard library under Terraform's names plus the common ones it lacks.
// Anything else, including the impure timestamp() and uuid(), is an unknown
// function, which makes the expression undetermined.
func functions() map[string]function.Function {
	return map[string]function.Function{
		"abs":             stdlib.AbsoluteFunc,
		"alltrue":         allTrueFunc,
		"anytrue":         anyTrueFunc,
		"base64decode":    stringFunc(base64Decode),
		"base64encode":    stringFunc(base64Encode),
		"can":             tryfunc.CanFunc,
		"ceil":            stdlib.CeilFunc,
		"chomp":           stdlib.ChompFunc,
		"chunklist":       stdlib.ChunklistFunc,
		"coalesce":        stdlib.CoalesceFunc,
		"coalescelist":    stdlib.CoalesceListFunc,
		"compact":         stdlib.CompactFunc,
		"concat":          stdlib.ConcatFunc,
		"contains":        stdlib.ContainsFunc,
		"csvdecode":       stdlib.CSVDecodeFunc,
		"distinct":        stdlib.DistinctFunc,
		"element":         stdlib.ElementFunc,
		"endswith":        stringPredicateFunc(strings.HasSuffix),
		"flatten":         stdlib.FlattenFunc,
		"floor":           stdlib.FloorFunc,
		"format":          stdlib.FormatFunc,
		"formatdate":      stdlib.FormatDateFunc,
		"formatlist":      stdlib.FormatListFunc,
		"indent":          stdlib.IndentFunc,
		"index":           stdlib.IndexFunc,
		"join":            stdlib.JoinFunc,
		"jsondecode":      stdlib.JSONDecodeFunc,
		"jsonencode":      stdlib.JSONEncodeFunc,
		"keys":            stdlib.KeysFunc,
		"length":          lengthFunc,
		"log":             stdlib.LogFunc,
		"lookup":          stdlib.LookupFunc,
		"lower":           stdlib.LowerFunc,
		"max":             stdlib.MaxFunc,
		"md5":             stringFunc(hexDigest(func(b []byte) []byte { s := md5.Sum(b); return s[:] })),
		"merge":           stdlib.MergeFunc,
		"min":             stdlib.MinFunc,
		"nonsensitive":    identityFunc,
		"one":             oneFunc,
		"parseint":        stdlib.ParseIntFunc,
		"pow":             stdlib.PowFunc,
		"range":           stdlib.RangeFunc,
		"regex":           stdlib.RegexFunc,
		"regexall":        stdlib.RegexAllFunc,
		"replace":         replaceFunc,
		"reverse":         stdlib.ReverseListFunc,
		"sensitive":       identityFunc,
		"setintersection": stdlib.SetIntersectionFunc,
		"setproduct":      stdlib.SetProductFunc,
		"setsubtract":     stdlib.SetSubtractFunc,
		"setunion":        stdlib.SetUnionFunc,
		"sha1":            stringFunc(hexDigest(func(b []byte) []byte { s := sha1.Sum(b); return s[:] })),
		"sha256":          stringFunc(hexDigest(func(b []byte) []byte { s := sha256.Sum256(b); return s[:] })),
		"sha512":          stringFunc(hexDigest(func(b []byte) []byte { s := sha512.Sum512(b); return s[:] })),
		"signum":          stdlib.SignumFunc,
		"slice":           stdlib.SliceFunc,
		"sort":            stdlib.SortFunc,
		"split":           stdlib.SplitFunc,
		"startswith":      stringPredicateFunc(strings.HasPrefix),
		"strcontains":     stringPredicateFunc(strings.Contains),
		"strrev":          stdlib.ReverseFunc,
		"substr":          stdlib.SubstrFunc,
		"sum":             sumFunc,
		"timeadd":         stdlib.TimeAddFunc,
		"title":           stdlib.TitleFunc,
		"tobool":          stdlib.MakeToFunc(cty.Bool),
		"tolist":          stdlib.MakeToFunc(cty.List(cty.DynamicPseudoType)),
		"tomap":           stdlib.MakeToFunc(cty.Map(cty.DynamicPseudoType)),
		"tonumber":        stdlib.MakeToFunc(cty.Number),
		"toset":           stdlib.MakeToFunc(cty.Set(cty.DynamicPseudoType)),
		"tostring":        stdlib.MakeToFunc(cty.String),
		"trim":            stdlib.TrimFunc,
		"trimprefix":      stdlib.TrimPrefixFunc,
		"trimspace":       stdlib.TrimSpaceFunc,
		"trimsuffix":      stdlib.TrimSuffixFunc,
		"try":             tryfunc.TryFunc,
		"upper":           stdlib.UpperFunc,
		"urlencode":       stringFunc(func(s string) (string, error) { return url.QueryEscape(s), nil }),
		"values":          stdlib.ValuesFunc,
		"zipmap":          stdlib.ZipmapFunc,
	}
}

func stringFunc(fn func(string) (string, error)) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "str", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			s, err := fn(args[0].AsString())
			if err != nil {
				return cty.NilVal, err
			}
			return cty.StringVal(s), nil
		},
	})
}

func stringPredicateFunc(fn func(s, sub string) bool) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "str", Type: cty.String}, {Name: "substr", Type: cty.String}},
		Type:   function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.BoolVal(fn(args[0].AsString(), args[1].AsString())), nil
		},
	})
}

func base64Encode(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}

func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

func hexDigest(sum func([]byte) []byte) func(string) (string, error) {
	return func(s string) (string, error) {
		return hex.EncodeToString(sum([]byte(s))), nil
	}
}

var identityFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType, AllowNull: true}},
	Type:   func(args []cty.Value) (cty.Type, error) { return args[0].Type(), nil },
	Impl:   func(args []cty.Value, _ cty.Type) (cty.Value, error) { return args[0], nil },
})

// lengthFunc also accepts a string, as Terraform's does.
var lengthFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType}},
	Type:   function.StaticReturnType(cty.Number),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		if args[0].Type() == cty.String {
			return stdlib.Strlen(args[0])
		}
		return stdlib.Length(args[0])
	},
})

// replaceFunc treats a /substring/ as a regular expression, as Terraform's does.
var replaceFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "str", Type: cty.String},
		{Name: "substr", Type: cty.String},
		{Name: "replace", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		str, substr, replace := args[0].AsString(), args[1].AsString(), args[2].AsString()
		if len(substr) > 1 && strings.HasPrefix(substr, "/") && strings.HasSuffix(substr, "/") {
			re, err := regexp.Compile(substr[1 : len(substr)-1])
			if err != nil {
				return cty.NilVal, err
			}
			return cty.StringVal(re.ReplaceAllString(str, replace)), nil
		}
		return cty.StringVal(strings.ReplaceAll(str, substr, replace)), nil
	},
})

var allTrueFunc = boolFoldFunc(true)
var anyTrueFunc = boolFoldFunc(false)

// boolFoldFunc implements alltrue (all true) and anytrue (all false). The first
// unknown element makes the result unknown, even if a later one would settle it.
func boolFoldFunc(all bool) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "list", Type: cty.List(cty.Bool)}},
		Type:   function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			for it := args[0].ElementIterator(); it.Next(); {
				_, v := it.Element()
				if !v.IsKnown() {
					return cty.UnknownVal(cty.Bool), nil
				}
				if v.IsNull() || v.True() != all {
					return cty.BoolVal(!all), nil
				}
			}
			return cty.BoolVal(all), nil
		},
	})
}

var oneFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "list", Type: cty.DynamicPseudoType}},
	Type:   function.StaticReturnType(cty.DynamicPseudoType),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		list, err := convert.Convert(args[0], cty.List(cty.DynamicPseudoType))
		if err != nil {
			return cty.NilVal, err
		}
		switch list.LengthInt() {
		case 0:
			return cty.NullVal(cty.DynamicPseudoType), nil
		case 1:
			return list.Index(cty.NumberIntVal(0)), nil
		}
		return cty.NilVal, function.NewArgErrorf(0, "must be a list, set, or tuple value with either zero or one elements")
	},
})

var sumFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "list", Type: cty.List(cty.Number)}},
	Type:   function.StaticReturnType(cty.Number),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		total := cty.Zero
		for it := args[0].ElementIterator(); it.Next(); {
			_, v := it.Element()
			if v.IsNull() {
				return cty.NilVal, function.NewArgErrorf(0, "argument must not contain null values")
			}
			total = total.Add(v)
		}
		return total, nil
	},
})
