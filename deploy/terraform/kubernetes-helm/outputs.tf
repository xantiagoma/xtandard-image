output "release" {
  description = "Helm release name and status."
  value       = "${helm_release.xi.name}: ${helm_release.xi.status}"
}

output "chart_values" {
  description = "The chart values this module passes (before extra_values); the check compares them with values.<fleet>.json."
  value       = local.values
}
