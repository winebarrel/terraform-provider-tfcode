package tfcode_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/terraform-provider-tfcode/internal/tfcode"
	"github.com/zclconf/go-cty/cty"
)

func read(t *testing.T, src string) *tfcode.Values {
	t.Helper()
	v, err := tfcode.Read(write(t, map[string]string{"main.tf": src}))
	require.NoError(t, err)
	return v
}

func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600))
	}
	return dir
}

func attrs(v cty.Value) map[string]cty.Value {
	return v.AsValueMap()
}

func TestLiteralValues(t *testing.T) {
	v := read(t, `
output "name" { value = "foo" }
locals { port = 80 }
resource "aws_route53_record" "myhost" {
  zone_id = "Z123"
  ttl     = 300
}
`)
	assert.Equal(t, cty.StringVal("foo"), attrs(v.Outputs)["name"])
	assert.True(t, attrs(v.Locals)["port"].RawEquals(cty.NumberIntVal(80)))

	record := v.Resource.GetAttr("aws_route53_record").GetAttr("myhost")
	assert.Equal(t, cty.StringVal("Z123"), record.GetAttr("zone_id"))
}

func TestStaticReferences(t *testing.T) {
	v := read(t, `
locals {
  env  = "prod"
  name = "${local.env}-web"
  tags = merge({ a = 1 }, { b = 2 })
}
resource "aws_route53_zone" "main" { name = "example.com" }
resource "aws_route53_record" "myhost" { name = aws_route53_zone.main.name }
output "name" { value = upper(local.name) }
`)
	assert.Equal(t, cty.StringVal("PROD-WEB"), attrs(v.Outputs)["name"])
	assert.True(t, attrs(v.Locals)["tags"].GetAttr("b").RawEquals(cty.NumberIntVal(2)))
	assert.Equal(t, cty.StringVal("example.com"),
		v.Resource.GetAttr("aws_route53_record").GetAttr("myhost").GetAttr("name"))
}

func TestUndeterminedValuesAreLeftOut(t *testing.T) {
	tests := map[string]string{
		"variable":             `var.x`,
		"variable in template": `"${var.x}-web"`,
		"variable with branch": `true ? "a" : var.x`,
		"data source":          `data.aws_ami.x.id`,
		"module":               `module.vpc.name`,
		"unset argument":       `aws_route53_zone.main.zone_id`,
		"whole resource":       `aws_route53_zone.main`,
		"undeclared resource":  `aws_vpc.nope.id`,
		"impure function":      `timestamp()`,
		"unknown function":     `nosuchfunc()`,
		"path":                 `path.module`,
		"invalid operation":    `1 + "a"`,
		"try on variable":      `try(var.x, "d")`,
		"try on unset":         `try(aws_route53_zone.main.zone_id, "d")`,
		"sequence with var":    `[1, var.x]`,
		"through a local":      `local.undetermined`,
	}
	for name, expr := range tests {
		t.Run(name, func(t *testing.T) {
			v := read(t, `
variable "x" { default = "not used" }
locals { undetermined = var.x }
resource "aws_route53_zone" "main" { name = "example.com" }
output "o" { value = `+expr+` }
`)
			assert.NotContains(t, attrs(v.Outputs), "o")
		})
	}
}

func TestUndeterminedDoesNotBecomeAWrongValue(t *testing.T) {
	v := read(t, `
variable "y" {}
locals {
  tags    = { a = "x", b = var.y }
  looked  = lookup(local.tags, "b", "default")
  merged  = merge(local.tags, { c = "z" })
  encoded = jsonencode(local.tags)
  keys    = keys(local.tags)
  caught  = try(local.tags.b, "default")
}
`)
	locals := attrs(v.Locals)
	assert.NotContains(t, locals, "looked")
	assert.NotContains(t, locals, "encoded")
	assert.NotContains(t, locals, "caught")
	assert.Equal(t, cty.StringVal("z"), locals["merged"].GetAttr("c"))
	assert.False(t, locals["merged"].Type().HasAttribute("b"))
	assert.Equal(t, 2, locals["keys"].LengthInt())
}

func TestPartialValues(t *testing.T) {
	v := read(t, `
resource "aws_route53_zone" "main" { name = "example.com" }
locals {
  tags = {
    a = "x"
    b = var.y
    c = aws_route53_zone.main.zone_id
    d = { e = "f", g = var.y }
  }
  list = [{ a = "x", b = var.y }]
}
`)
	tags := attrs(v.Locals)["tags"]
	assert.Equal(t, cty.StringVal("x"), tags.GetAttr("a"))
	assert.False(t, tags.Type().HasAttribute("b"))
	assert.False(t, tags.Type().HasAttribute("c"))
	assert.Equal(t, cty.StringVal("f"), tags.GetAttr("d").GetAttr("e"))
	assert.False(t, tags.GetAttr("d").Type().HasAttribute("g"))

	first := attrs(v.Locals)["list"].AsValueSlice()[0]
	assert.Equal(t, cty.StringVal("x"), first.GetAttr("a"))
	assert.False(t, first.Type().HasAttribute("b"))
}

func TestCount(t *testing.T) {
	v := read(t, `
resource "aws_route53_record" "a" {
  count = 2
  name  = "host-${count.index}"
}
resource "aws_route53_record" "unknown" {
  count = var.n
  name  = "x"
}
output "second" { value = aws_route53_record.a[1].name }
output "whole" { value = aws_route53_record.a[1] }
`)
	records := v.Resource.GetAttr("aws_route53_record")
	assert.Equal(t, cty.StringVal("host-0"), records.GetAttr("a").AsValueSlice()[0].GetAttr("name"))
	assert.False(t, records.GetAttr("a").AsValueSlice()[0].Type().HasAttribute("count"))
	assert.False(t, records.Type().HasAttribute("unknown"))
	assert.Equal(t, cty.StringVal("host-1"), attrs(v.Outputs)["second"])
	assert.NotContains(t, attrs(v.Outputs), "whole")
}

func TestForEach(t *testing.T) {
	v := read(t, `
resource "aws_route53_record" "set" {
  for_each = toset(["x", "y"])
  name     = each.key
}
resource "aws_route53_record" "map" {
  for_each = { x = "one", y = var.z }
  name     = each.key
  ttl      = each.value
}
output "o" { value = aws_route53_record.set["y"].name }
`)
	records := v.Resource.GetAttr("aws_route53_record")
	assert.Equal(t, cty.StringVal("x"), records.GetAttr("set").GetAttr("x").GetAttr("name"))
	assert.Equal(t, cty.StringVal("one"), records.GetAttr("map").GetAttr("x").GetAttr("ttl"))
	assert.False(t, records.GetAttr("map").GetAttr("y").Type().HasAttribute("ttl"))
	assert.Equal(t, cty.StringVal("y"), attrs(v.Outputs)["o"])
}

func TestBlocks(t *testing.T) {
	v := read(t, `
resource "aws_security_group" "web" {
  ingress { from_port = 80 }
  ingress {
    from_port = 443
    to_port   = var.p
  }
  timeouts { create = "5m" }
  lifecycle { create_before_destroy = true }
  dynamic "egress" {
    for_each = [1, 2]
    iterator = rule
    content {
      to_port = rule.value + rule.key
    }
  }
  dynamic "unknown" {
    for_each = var.rules
    content {}
  }
}
`)
	sg := v.Resource.GetAttr("aws_security_group").GetAttr("web")
	ingress := sg.GetAttr("ingress").AsValueSlice()
	assert.True(t, ingress[1].GetAttr("from_port").RawEquals(cty.NumberIntVal(443)))
	assert.False(t, ingress[1].Type().HasAttribute("to_port"))
	assert.Equal(t, cty.StringVal("5m"), sg.GetAttr("timeouts").AsValueSlice()[0].GetAttr("create"))
	assert.False(t, sg.Type().HasAttribute("lifecycle"))
	assert.True(t, sg.GetAttr("egress").AsValueSlice()[1].GetAttr("to_port").RawEquals(cty.NumberIntVal(3)))
	assert.False(t, sg.Type().HasAttribute("unknown"))
}

func TestFunctions(t *testing.T) {
	tests := map[string]cty.Value{
		`replace("abc", "/[ab]/", "x")`: cty.StringVal("xxc"),
		`replace("a.b", ".", "-")`:      cty.StringVal("a-b"),
		`startswith("abc", "a")`:        cty.True,
		`endswith("abc", "a")`:          cty.False,
		`strcontains("abc", "b")`:       cty.True,
		`length("abc")`:                 cty.NumberIntVal(3),
		`length(["a", "b"])`:            cty.NumberIntVal(2),
		`alltrue([true, false])`:        cty.False,
		`anytrue([true, false])`:        cty.True,
		`one(["a"])`:                    cty.StringVal("a"),
		`sum([1, 2])`:                   cty.NumberIntVal(3),
		`base64encode("a")`:             cty.StringVal("YQ=="),
		`base64decode("YQ==")`:          cty.StringVal("a"),
		`urlencode("a b")`:              cty.StringVal("a+b"),
		`md5("a")`:                      cty.StringVal("0cc175b9c0f1b6a831c399e269772661"),
		`sensitive("a")`:                cty.StringVal("a"),
		`try(nosuch.value, "d")`:        cty.NilVal,
		`lookup({ a = 1 }, "b", 2)`:     cty.NumberIntVal(2),
		`tostring(1)`:                   cty.StringVal("1"),
	}
	for expr, want := range tests {
		t.Run(expr, func(t *testing.T) {
			v := read(t, `output "o" { value = `+expr+` }`)
			got, ok := attrs(v.Outputs)["o"]
			if want == cty.NilVal {
				assert.False(t, ok)
				return
			}
			require.True(t, ok)
			assert.True(t, got.RawEquals(want), "got %#v", got)
		})
	}
}

func TestFiles(t *testing.T) {
	dir := write(t, map[string]string{
		"a.tf":      `output "a" { value = local.b }`,
		"b.tf":      `locals { b = "from b" }`,
		".draft.tf": `this does not parse`,
		"ignore.md": `this does not parse`,
	})
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "c.tf"), []byte(`output "c" { value = 1 }`), 0o600))

	v, err := tfcode.Read(dir)
	require.NoError(t, err)
	assert.Equal(t, cty.StringVal("from b"), attrs(v.Outputs)["a"])
	assert.NotContains(t, attrs(v.Outputs), "c")
}

func TestErrors(t *testing.T) {
	tests := map[string]struct{ src, want string }{
		"syntax":           {`output {`, "main.tf"},
		"duplicate local":  {"locals { a = 1 }\nlocals { a = 2 }", `duplicate local "a"`},
		"duplicate output": {"output \"a\" { value = 1 }\noutput \"a\" { value = 2 }", `duplicate output "a"`},
		"duplicate resource": {
			"resource \"x\" \"a\" {}\nresource \"x\" \"a\" {}",
			"duplicate resource x.a",
		},
		"circular local": {"locals {\n a = local.b\n b = local.a\n}", "circular reference"},
		"circular resource": {
			"resource \"x\" \"a\" { n = x.b.n }\nresource \"x\" \"b\" { n = x.a.n }",
			"circular reference",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := tfcode.Read(write(t, map[string]string{"main.tf": tt.src}))
			assert.ErrorContains(t, err, tt.want)
		})
	}

	_, err := tfcode.Read(t.TempDir())
	assert.ErrorContains(t, err, "no .tf files")
}
