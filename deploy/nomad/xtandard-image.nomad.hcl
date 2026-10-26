# HashiCorp Nomad job, Docker driver (docs/DEPLOY.md "Nomad").
#
#   nomad var put nomad/jobs/xtandard-image signing_keys=<hex> signing_salts=<hex>
#   nomad job run deploy/nomad/xtandard-image.nomad.hcl
#
# Autoscaling: the `scaling` block sets min/max; the Nomad Autoscaler (a separate agent) applies
# the policy below, reading the server's own metrics from Prometheus. Without the autoscaler,
# `count` is fixed.
job "xtandard-image" {
  type = "service"

  update {
    max_parallel      = 1
    health_check      = "checks"
    min_healthy_time  = "10s"
    healthy_deadline  = "2m"
    progress_deadline = "5m"
    auto_revert       = true
    canary            = 0
  }

  group "image" {
    count = 2

    scaling {
      enabled = true
      min     = 2
      max     = 20

      policy {
        cooldown            = "2m"
        evaluation_interval = "30s"

        # Average busy workers per instance: keep about 70 % of the workers busy.
        check "workers_busy" {
          source = "prometheus"
          query  = "avg(xi_workers_busy{job=\"xtandard-image\"})"

          strategy "target-value" {
            target = 1.4
          }
        }
      }
    }

    network {
      port "http" { to = 8080 }
      port "admin" { to = 9090 }
    }

    service {
      name     = "xtandard-image"
      port     = "http"
      provider = "nomad"

      check {
        name     = "ready"
        type     = "http"
        path     = "/readyz"
        interval = "5s"
        timeout  = "2s"
      }
    }

    service {
      name     = "xtandard-image-metrics"
      port     = "admin"
      provider = "nomad"
      tags     = ["metrics"]
    }

    task "xtandard-image" {
      driver = "docker"

      # SIGTERM → /readyz 503 → 5 s → drain up to 30 s.
      kill_signal  = "SIGTERM"
      kill_timeout = "40s"
      # Deregister from the service catalog before the signal, so new requests stop first.
      shutdown_delay = "5s"

      config {
        image     = "ghcr.io/xantiagoma/xtandard-image:0.1.1-slim"
        ports     = ["http", "admin"]
        readonly_rootfs = true
        cap_drop  = ["ALL"]
        security_opt = ["no-new-privileges"]

        mount {
          type   = "tmpfs"
          target = "/tmp"
          readonly = false
          tmpfs_options {
            size = 67108864
          }
        }
      }

      template {
        destination = "secrets/xi.env"
        env         = true
        data        = <<-EOT
          {{ with nomadVar "nomad/jobs/xtandard-image" }}
          XI_SIGNING_KEYS={{ .signing_keys }}
          XI_SIGNING_SALTS={{ .signing_salts }}
          {{ end }}
        EOT
      }

      env {
        XI_ADMIN_ADDR           = ":9090"
        XI_SOURCES              = "web"
        XI_SOURCES_WEB_TYPE     = "http"
        XI_SOURCES_WEB_ALLOW    = "images.example.com"
        XI_CACHE_RESULT_STORES  = "memory"
      }

      # Workers follow the CPU limit (cores = 2); GOMEMLIMIT follows memory_max.
      resources {
        cores      = 2
        memory     = 2048
        memory_max = 3072
      }
    }
  }
}
