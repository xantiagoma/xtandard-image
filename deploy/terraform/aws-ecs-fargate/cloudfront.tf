# Optional CloudFront distribution in front of the ALB (docs/OPERATIONS.md has the CDN guidance).
# The cache key includes the Accept header so format negotiation (AVIF/WebP by Accept) is safe;
# results already carry long Cache-Control (XI_CACHE_HEADERS_TTL), which CloudFront honours.

resource "aws_cloudfront_cache_policy" "xi" {
  count       = var.cloudfront ? 1 : 0
  name        = "${var.name}-images"
  min_ttl     = 0
  default_ttl = 86400
  max_ttl     = 31536000

  parameters_in_cache_key_and_forwarded_to_origin {
    enable_accept_encoding_brotli = false
    enable_accept_encoding_gzip   = false

    headers_config {
      header_behavior = "whitelist"
      headers {
        items = ["Accept"]
      }
    }
    cookies_config {
      cookie_behavior = "none"
    }
    # Query strings matter for the query dialect (/q/?url=…) and imgix-style mounts.
    query_strings_config {
      query_string_behavior = "all"
    }
  }
}

resource "aws_cloudfront_distribution" "xi" {
  count           = var.cloudfront ? 1 : 0
  enabled         = true
  is_ipv6_enabled = true
  http_version    = "http2and3"
  price_class     = "PriceClass_100"
  comment         = var.name

  origin {
    origin_id   = "alb"
    domain_name = aws_lb.xi.dns_name

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = var.certificate_arn == "" ? "http-only" : "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
      origin_read_timeout    = 30
    }
  }

  default_cache_behavior {
    target_origin_id       = "alb"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    cache_policy_id        = aws_cloudfront_cache_policy.xi[0].id
    compress               = false
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}
