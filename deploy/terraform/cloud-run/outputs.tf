output "url" {
  description = "The service URL."
  value       = google_cloud_run_v2_service.xi.uri
}
