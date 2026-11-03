# terraform-provider-tfcode

[![CI](https://github.com/winebarrel/terraform-provider-tfcode/actions/workflows/ci.yml/badge.svg)](https://github.com/winebarrel/terraform-provider-tfcode/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/winebarrel/terraform-provider-tfcode/graph/badge.svg?token=hmmUSarOJW)](https://codecov.io/gh/winebarrel/terraform-provider-tfcode)
[![AI Generated](https://img.shields.io/badge/AI%20Generated-Claude-orange?logo=anthropic)](https://claude.com/claude-code)

A Terraform data source that reads static values from the `.tf` files of another directory, with an interface like `terraform_remote_state`. It never reads a state file.

```hcl
terraform {
  required_providers {
    tfcode = {
      source = "winebarrel/tfcode"
    }
  }
}

data "tfcode" "dns" {
  source = "../dns"
}

output "zone_id" {
  value = data.tfcode.dns.resource.aws_route53_record.myhost.zone_id
}

output "ttl" {
  value = data.tfcode.dns.outputs.ttl
}

output "domain" {
  value = data.tfcode.dns.locals.domain
}
```

`source` is a relative path to a directory, and only the `.tf` files directly under it are read. It is resolved against the directory Terraform runs in, because a provider is not told where the calling file is. Inside a module, write `"${path.module}/../dns"`.

## What can be read

Expressions are evaluated when the source alone decides their value: literals, `local` references, functions, arguments written on other resources, `count`, `for_each` and `dynamic` blocks.

Reading anything else fails with `Unsupported attribute`. That includes expressions using `var` (even with a default), `data` or `module`, and attributes the source does not set, such as a provider-computed `id`. An undetermined value causes no error until you refer to it, and the rest of the object stays readable:

```hcl
# ../dns
locals {
  tags = {
    a = "x"
    b = var.y
  }
}
```

```hcl
data.tfcode.dns.locals.tags.a # "x"
data.tfcode.dns.locals.tags.b # Unsupported attribute
```

See the [data source documentation](docs/data-sources/tfcode.md) for the details, the limitations and the list of supported functions.

## Development

```console
$ make test        # unit and acceptance tests
$ make lint        # golangci-lint
$ make docs        # regenerate docs/
$ make tf-console  # terraform console against a locally built provider
```

`make tf-console` reads `tfcode.tf` in the repository root, which is gitignored. `tfcode.tf.sample` is there to copy.
