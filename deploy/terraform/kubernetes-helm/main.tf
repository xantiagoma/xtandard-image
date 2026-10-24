# Kubernetes through the repository's Helm chart (deploy/helm/xtandard-image, WP-148).
# docs/DEPLOY.md "Terraform: Kubernetes (Helm)"; the chart and its values are documented in
# docs/OPERATIONS.md. Every chart value this module sets is in local.values below.

locals {
  chart_path = "${path.module}/../../helm/xtandard-image"

  # Chart values (names from the WP-148 chart; adjust here if the chart renames them).
  values = {
    role = var.role
    image = {
      tag = var.image_tag
    }
    autoscaling = {
      enabled     = true
      minReplicas = var.min_replicas
      maxReplicas = var.max_replicas
    }
  }
}

resource "kubernetes_namespace_v1" "xi" {
  metadata {
    name = var.namespace
  }
}

# Signing keys as a Secret; the chart is pointed at it through var.extra_values.
resource "kubernetes_secret_v1" "signing" {
  metadata {
    name      = "xi-signing"
    namespace = kubernetes_namespace_v1.xi.metadata[0].name
  }
  data = {
    XI_SIGNING_KEYS  = var.signing_keys
    XI_SIGNING_SALTS = var.signing_salts
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
    yamlencode(local.values),
    yamlencode(var.extra_values),
  ]

  depends_on = [kubernetes_secret_v1.signing]
}
