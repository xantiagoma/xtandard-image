output "alb_dns_name" {
  description = "Load balancer DNS name."
  value       = aws_lb.xi.dns_name
}

output "cloudfront_domain_name" {
  description = "CloudFront domain (empty when disabled)."
  value       = var.cloudfront ? aws_cloudfront_distribution.xi[0].domain_name : ""
}
