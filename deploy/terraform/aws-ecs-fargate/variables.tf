variable "region" {
  description = "AWS region."
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "Name of the cluster, service and load balancer."
  type        = string
  default     = "xtandard-image"
}

variable "vpc_id" {
  description = "VPC for the load balancer and tasks."
  type        = string
}

variable "public_subnet_ids" {
  description = "Subnets for the ALB (at least two AZs)."
  type        = list(string)
}

variable "private_subnet_ids" {
  description = "Subnets for the tasks (with a NAT or VPC endpoints for image pulls and origins)."
  type        = list(string)
}

variable "certificate_arn" {
  description = "ACM certificate for an HTTPS listener; empty serves HTTP on port 80."
  type        = string
  default     = ""
}

variable "image" {
  description = "Container image (ghcr.io needs registry_credentials_arn while the package is private)."
  type        = string
  default     = "ghcr.io/xantiagoma/xtandard-image:0.1.0-slim"
}

variable "registry_credentials_arn" {
  description = "Secrets Manager secret with {\"username\",\"password\"} for a private registry; empty for none."
  type        = string
  default     = ""
}

variable "cpu" {
  description = "Task CPU units (1024 = 1 vCPU); the worker count follows it."
  type        = number
  default     = 2048
}

variable "memory" {
  description = "Task memory (MiB); GOMEMLIMIT follows it."
  type        = number
  default     = 4096
}

variable "cpu_architecture" {
  description = "ARM64 (Graviton) or X86_64; the image is multi-arch."
  type        = string
  default     = "ARM64"
}

variable "min_tasks" {
  description = "Minimum tasks (Fargate services do not scale to zero on demand)."
  type        = number
  default     = 2
}

variable "max_tasks" {
  description = "Maximum tasks."
  type        = number
  default     = 20
}

variable "cpu_target" {
  description = "Target average CPU utilization (%)."
  type        = number
  default     = 60
}

variable "requests_per_target" {
  description = "Target ALB requests per task per minute; 0 disables the second policy."
  type        = number
  default     = 0
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

variable "cloudfront" {
  description = "Create a CloudFront distribution in front of the ALB."
  type        = bool
  default     = true
}
