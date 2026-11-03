data "tfcode" "dns" {
  source = "../dns"
}

resource "aws_route53_record" "www" {
  zone_id = data.tfcode.dns.resource.aws_route53_record.myhost.zone_id
  name    = "www.${data.tfcode.dns.locals.domain}"
  type    = "CNAME"
  ttl     = data.tfcode.dns.outputs.ttl
  records = ["example.com"]
}
