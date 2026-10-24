# Azure Container Apps with KEDA HTTP and CPU scale rules (docs/DEPLOY.md
# "Terraform: Azure Container Apps"). Same settings as deploy/azure-container-apps/containerapp.yaml.

resource "azurerm_resource_group" "xi" {
  name     = var.resource_group
  location = var.location
}

resource "azurerm_log_analytics_workspace" "xi" {
  name                = "${var.name}-logs"
  location            = azurerm_resource_group.xi.location
  resource_group_name = azurerm_resource_group.xi.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
}

resource "azurerm_container_app_environment" "xi" {
  name                       = "${var.name}-env"
  location                   = azurerm_resource_group.xi.location
  resource_group_name        = azurerm_resource_group.xi.name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.xi.id
}

resource "azurerm_container_app" "xi" {
  name                         = var.name
  container_app_environment_id = azurerm_container_app_environment.xi.id
  resource_group_name          = azurerm_resource_group.xi.name
  revision_mode                = "Single"

  # Values come from variables marked sensitive (TF_VAR_*); prefer Key Vault references.
  secret {
    name  = "xi-signing-keys"
    value = var.signing_keys
  }
  secret {
    name  = "xi-signing-salts"
    value = var.signing_salts
  }
  dynamic "secret" {
    for_each = var.registry_password == "" ? [] : [1]
    content {
      name  = "registry-password"
      value = var.registry_password
    }
  }

  dynamic "registry" {
    for_each = var.registry_password == "" ? [] : [1]
    content {
      server               = "ghcr.io"
      username             = var.registry_username
      password_secret_name = "registry-password"
    }
  }

  ingress {
    external_enabled = true
    target_port      = 8080
    transport        = "auto"
    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }

  template {
    min_replicas = var.min_replicas
    max_replicas = var.max_replicas
    # SIGTERM → /readyz 503 → 5 s → drain up to 30 s.
    termination_grace_period_seconds = 40

    # One replica per `concurrent_requests` requests in flight.
    http_scale_rule {
      name                = "http-concurrency"
      concurrent_requests = tostring(var.concurrent_requests)
    }

    custom_scale_rule {
      name             = "cpu"
      custom_rule_type = "cpu"
      metadata = {
        type  = "Utilization"
        value = tostring(var.cpu_target)
      }
    }

    container {
      name   = "xtandard-image"
      image  = var.image
      cpu    = var.cpu
      memory = var.memory

      env {
        name        = "XI_SIGNING_KEYS"
        secret_name = "xi-signing-keys"
      }
      env {
        name        = "XI_SIGNING_SALTS"
        secret_name = "xi-signing-salts"
      }
      dynamic "env" {
        for_each = merge({
          XI_SOURCES             = "web"
          XI_SOURCES_WEB_TYPE    = "http"
          XI_SOURCES_WEB_ALLOW   = var.allowed_hosts
          XI_CACHE_RESULT_STORES = "memory"
        }, var.extra_env)
        content {
          name  = env.key
          value = env.value
        }
      }

      startup_probe {
        transport               = "HTTP"
        port                    = 8080
        path                    = "/readyz"
        interval_seconds        = 1
        failure_count_threshold = 30
      }
      readiness_probe {
        transport               = "HTTP"
        port                    = 8080
        path                    = "/readyz"
        interval_seconds        = 5
        failure_count_threshold = 2
      }
      liveness_probe {
        transport               = "HTTP"
        port                    = 8080
        path                    = "/healthz"
        interval_seconds        = 10
        failure_count_threshold = 3
      }

      volume_mounts {
        name = "tmp"
        path = "/tmp"
      }
    }

    volume {
      name         = "tmp"
      storage_type = "EmptyDir"
    }
  }
}
