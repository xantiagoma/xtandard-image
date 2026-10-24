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

variable "fleet" {
  description = "Which values file to install: image (values.image.json) or video (values.video.json); one release per fleet."
  type        = string
  default     = "image"

  validation {
    condition     = contains(["image", "video"], var.fleet)
    error_message = "fleet must be image or video."
  }
}

variable "image_flavor" {
  description = "Chart image.flavor (slim, full, gpl); null keeps the values file's. The video fleet needs full or gpl."
  type        = string
  default     = null

  validation {
    condition     = var.image_flavor == null ? true : contains(["slim", "full", "gpl"], var.image_flavor)
    error_message = "image_flavor must be slim, full or gpl."
  }
}

variable "image_tag" {
  description = "Chart image.tag; empty or null uses <appVersion>-<flavor>. A tag must end in -<flavor> so the chart's flavor checks stay true."
  type        = string
  default     = null
}

variable "autoscaling_type" {
  description = "hpa (CPU) or keda (the server's queue metrics; KEDA and Prometheus installed); null keeps the file's."
  type        = string
  default     = null
}

variable "min_replicas" {
  description = "autoscaling.minReplicas; null keeps the file's."
  type        = number
  default     = null
}

variable "max_replicas" {
  description = "autoscaling.maxReplicas; null keeps the file's."
  type        = number
  default     = null
}

variable "sources" {
  description = "XI_SOURCES and its XI_SOURCES_<NAME>_* keys (non-secret), replacing the file's; credentials go in the Secret. Example: { XI_SOURCES = \"origin\", XI_SOURCES_ORIGIN_TYPE = \"http\", XI_SOURCES_ORIGIN_ALLOW = \"images.example.com\" }."
  type        = map(string)
  default     = null

  validation {
    condition     = var.sources == null ? true : contains(keys(var.sources), "XI_SOURCES")
    error_message = "sources must contain XI_SOURCES."
  }
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

variable "stream_keys" {
  description = "XI_VIDEO_TRANSFORM_STREAM_KEYS (hex), the same on every video pod."
  type        = string
  sensitive   = true
}

variable "extra_values" {
  description = "More chart values, merged after the module's own (see the chart's values.yaml)."
  type        = any
  default     = {}
}
