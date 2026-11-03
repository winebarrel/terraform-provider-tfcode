variable "y" {}

locals {
  env = "prod"
  tags = {
    a = "x"
    b = var.y
  }
  nothing = null
  ports   = [80, 443]
  names   = toset(["a", "b"])
}

resource "aws_route53_zone" "main" {
  name = "example.com"
}

resource "aws_route53_record" "myhost" {
  zone_id = "Z123"
  name    = "myhost.${aws_route53_zone.main.name}"
  ttl     = 300
  proxied = false
  fqdn    = aws_route53_zone.main.zone_id
}

resource "aws_route53_record" "many" {
  count = 2
  name  = "host-${count.index}"
}

resource "aws_security_group" "web" {
  ingress {
    from_port = 80
  }
  ingress {
    from_port = 443
  }
}

output "env" {
  value = local.env
}

output "secret" {
  value     = "shown"
  sensitive = true
}

output "undetermined" {
  value = var.y
}
