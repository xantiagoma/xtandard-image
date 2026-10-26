variable "subscription_id" {
  description = "Azure subscription ID."
  type        = string
}

variable "location" {
  description = "Azure region."
  type        = string
  default     = "eastus"
}

variable "resource_group" {
  description = "Resource group to create."
  type        = string
  default     = "xtandard-image"
}

variable "name" {
  description = "Container app name."
  type        = string
  default     = "xtandard-image"
}

variable "image" {
  description = "Container image."
  type        = string
  default     = "ghcr.io/xantiagoma/xtandard-image:0.1.1-slim"
}

variable "registry_username" {
  description = "GHCR user for a private package."
  type        = string
  default     = "xantiagoma"
}

variable "registry_password" {
  description = "GHCR token (read:packages); empty for a public image."
  type        = string
  default     = ""
  sensitive   = true
}

variable "signing_keys" {
  description = "XI_SIGNING_KEYS (hex)."
  type        = string
  sensitive   = true
}

variable "signing_salts" {
  description = "XI_SIGNING_SALTS (hex)."
  type        = string
  sensitive   = true
}

variable "cpu" {
  description = "vCPUs per replica (Consumption: 0.25 steps up to 4); workers follow it."
  type        = number
  default     = 2
}

variable "memory" {
  description = "Memory per replica, twice the vCPUs on Consumption (e.g. 4Gi for 2)."
  type        = string
  default     = "4Gi"
}

variable "min_replicas" {
  description = "Minimum replicas (0 = scale to zero; the CPU rule then only acts above 0)."
  type        = number
  default     = 1
}

variable "max_replicas" {
  description = "Maximum replicas (up to 1000)."
  type        = number
  default     = 20
}

variable "concurrent_requests" {
  description = "In-flight requests per replica before another is added (about 2x vCPUs)."
  type        = number
  default     = 8
}

variable "cpu_target" {
  description = "CPU utilization target (%) of the custom KEDA cpu rule."
  type        = number
  default     = 65
}

variable "allowed_hosts" {
  description = "XI_SOURCES_WEB_ALLOW: comma-separated host globs of the http source."
  type        = string
  default     = "images.example.com"
}

variable "extra_env" {
  description = "More XI_* variables (docs/CONFIG.md)."
  type        = map(string)
  default     = {}
}
