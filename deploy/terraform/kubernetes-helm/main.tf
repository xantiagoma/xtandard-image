# Kubernetes through the repository's Helm chart (deploy/helm/xtandard-image, WP-148).
# docs/DEPLOY.md "Terraform / OpenTofu"; the chart is documented in docs/OPERATIONS.md.
#
# The chart values live in values.<fleet>.json (one place; `task deploy:platforms:check` renders
# them through helm template + kubeconform and checks that local.values equals the file when no
# override variable is set). Variables left null keep the file's value.

locals {
  chart_path = "${path.module}/../../helm/xtandard-image"
  base       = jsondecode(file("${path.module}/values.${var.fleet}.json"))

  image = merge(local.base.image, { for k, v in {
    flavor = var.image_flavor
    tag    = var.image_tag
  } : k => v if v != null })

  autoscaling = merge(local.base.autoscaling, { for k, v in {
    type        = var.autoscaling_type
    minReplicas = var.min_replicas
    maxReplicas = var.max_replicas
  } : k => v if v != null })

  values = merge(local.base, {
    image = local.image
    config = merge(
      { for k, v in local.base.config : k => v if var.sources == null || !startswith(k, "XI_SOURCES") },
      var.sources != null ? var.sources : tomap({}),
    )
    autoscaling = local.autoscaling
  })
}

resource "kubernetes_namespace_v1" "xi" {
  metadata {
    name = var.namespace
  }
}

# The chart loads secrets only from existingSecret: this is that Secret.
resource "kubernetes_secret_v1" "signing" {
  metadata {
    name      = local.values.existingSecret
    namespace = kubernetes_namespace_v1.xi.metadata[0].name
  }
  data = {
    XI_SIGNING_KEYS                = var.signing_keys
    XI_SIGNING_SALTS               = var.signing_salts
    XI_VIDEO_TRANSFORM_STREAM_KEYS = var.stream_keys
  }
}

resource "helm_release" "xi" {
  name      = var.release_name
  namespace = kubernetes_namespace_v1.xi.metadata[0].name
  chart     = local.chart_path
  atomic    = true
  wait      = true
  timeout   = 600

  values = [
    jsonencode(local.values),
    yamlencode(var.extra_values),
  ]

  lifecycle {
    precondition {
      # The chart's checks (video needs full or gpl) read image.flavor; a tag of another
      # flavour would pass them and run the wrong image.
      condition     = local.image.tag == "" || endswith(local.image.tag, "-${local.image.flavor}")
      error_message = "image_tag must end in -<image flavor> (or stay empty for <appVersion>-<flavor>)."
    }
    precondition {
      condition     = local.values.role != "video" || local.image.flavor != "slim"
      error_message = "the video fleet needs image_flavor full or gpl."
    }
  }

  depends_on = [kubernetes_secret_v1.signing]
}
