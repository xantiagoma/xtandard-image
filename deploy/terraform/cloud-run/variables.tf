variable "project" {
  description = "Google Cloud project ID."
  type        = string
}

variable "region" {
  description = "Cloud Run region."
  type        = string
  default     = "us-central1"
}

variable "name" {
  description = "Service name."
  type        = string
  default     = "xtandard-image"
}

variable "image" {
  description = "Image in Artifact Registry (a remote repository mirroring ghcr.io works)."
  type        = string
  default     = "us-docker.pkg.dev/PROJECT/ghcr/xantiagoma/xtandard-image:0.1.0-rc.2-slim"
}

variable "min_instances" {
  description = "Minimum instances (0 = scale to zero)."
  type        = number
  default     = 1
}

variable "max_instances" {
  description = "Maximum instances."
  type        = number
  default     = 20
}

variable "concurrency" {
  description = "Requests in flight per instance before Cloud Run adds one (about 2-4x vCPUs)."
  type        = number
  default     = 16
}

variable "cpu" {
  description = "vCPUs per instance; the worker count follows it."
  type        = string
  default     = "2"
}

variable "memory" {
  description = "Memory per instance; GOMEMLIMIT follows it."
  type        = string
  default     = "2Gi"
}

variable "allowed_hosts" {
  description = "XI_SOURCES_WEB_ALLOW: comma-separated host globs of the http source."
  type        = string
  default     = "images.example.com"
}

variable "extra_env" {
  description = "More XI_* variables (docs/CONFIG.md), e.g. XI_ROLE for a video service."
  type        = map(string)
  default     = {}
}

variable "public" {
  description = "Grant roles/run.invoker to allUsers."
  type        = bool
  default     = true
}
