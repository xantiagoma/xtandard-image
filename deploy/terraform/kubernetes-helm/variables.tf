variable "kubeconfig_path" {
  description = "Path of the kubeconfig file."
  type        = string
  default     = "~/.kube/config"
}

variable "kube_context" {
  description = "kubeconfig context; empty uses the current one."
  type        = string
  default     = ""
}

variable "namespace" {
  description = "Namespace to create."
  type        = string
  default     = "xtandard-image"
}

variable "release_name" {
  description = "Helm release name."
  type        = string
  default     = "xtandard-image"
}

variable "role" {
  description = "XI_ROLE of this release: all, image or video (one release per fleet)."
  type        = string
  default     = "all"

  validation {
    condition     = contains(["all", "image", "video"], var.role)
    error_message = "role must be all, image or video."
  }
}

variable "image_tag" {
  description = "Image tag, <version>-slim|-full|-gpl."
  type        = string
  default     = "0.1.0-slim"
}

variable "min_replicas" {
  description = "Autoscaler minimum."
  type        = number
  default     = 2
}

variable "max_replicas" {
  description = "Autoscaler maximum."
  type        = number
  default     = 20
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

variable "extra_values" {
  description = "More chart values, merged after the module's own (see the chart's values.yaml)."
  type        = any
  default     = {}
}
