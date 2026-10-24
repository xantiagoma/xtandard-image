# Google Cloud Run (v2 API) service for xtandard-image (docs/DEPLOY.md "Terraform: Cloud Run").
# Same settings as deploy/cloud-run/service.yaml.

resource "google_secret_manager_secret" "xi" {
  for_each  = toset(["xi-signing-keys", "xi-signing-salts"])
  secret_id = each.key
  replication {
    auto {}
  }
}

# Secret values are added out of band (gcloud secrets versions add), never in state.

resource "google_service_account" "xi" {
  account_id   = "xtandard-image"
  display_name = "xtandard-image"
}

resource "google_secret_manager_secret_iam_member" "xi" {
  for_each  = google_secret_manager_secret.xi
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.xi.email}"
}

resource "google_cloud_run_v2_service" "xi" {
  name                = var.name
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false

  template {
    service_account                  = google_service_account.xi.email
    execution_environment            = "EXECUTION_ENVIRONMENT_GEN2"
    timeout                          = "30s"
    max_instance_request_concurrency = var.concurrency

    scaling {
      min_instance_count = var.min_instances
      max_instance_count = var.max_instances
    }

    containers {
      image = var.image

      ports {
        name           = "http1"
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }
        # false = CPU always allocated (instance-based billing).
        cpu_idle          = false
        startup_cpu_boost = true
      }

      dynamic "env" {
        for_each = {
          XI_SIGNING_KEYS  = google_secret_manager_secret.xi["xi-signing-keys"].secret_id
          XI_SIGNING_SALTS = google_secret_manager_secret.xi["xi-signing-salts"].secret_id
        }
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      dynamic "env" {
        for_each = merge({
          XI_SOURCES                 = "web"
          XI_SOURCES_WEB_TYPE        = "http"
          XI_SOURCES_WEB_ALLOW       = var.allowed_hosts
          XI_CACHE_RESULT_STORES     = "memory"
          XI_SERVER_SHUTDOWN_DELAY   = "1s"
          XI_SERVER_SHUTDOWN_TIMEOUT = "8s"
        }, var.extra_env)
        content {
          name  = env.key
          value = env.value
        }
      }

      startup_probe {
        period_seconds    = 1
        timeout_seconds   = 1
        failure_threshold = 30
        http_get {
          path = "/readyz"
          port = 8080
        }
      }

      liveness_probe {
        period_seconds    = 10
        timeout_seconds   = 3
        failure_threshold = 3
        http_get {
          path = "/healthz"
          port = 8080
        }
      }

      volume_mounts {
        name       = "tmp"
        mount_path = "/tmp"
      }
    }

    volumes {
      name = "tmp"
      empty_dir {
        medium     = "MEMORY"
        size_limit = "64Mi"
      }
    }
  }

  depends_on = [google_secret_manager_secret_iam_member.xi]
}

# Public service: anyone may invoke it (put a CDN or load balancer in front for caching).
resource "google_cloud_run_v2_service_iam_member" "public" {
  count    = var.public ? 1 : 0
  name     = google_cloud_run_v2_service.xi.name
  location = google_cloud_run_v2_service.xi.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}
