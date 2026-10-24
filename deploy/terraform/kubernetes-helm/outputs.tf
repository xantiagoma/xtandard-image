output "release" {
  description = "Helm release name and status."
  value       = "${helm_release.xi.name}: ${helm_release.xi.status}"
}
