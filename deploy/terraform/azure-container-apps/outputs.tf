output "fqdn" {
  description = "Ingress FQDN of the latest revision."
  value       = azurerm_container_app.xi.latest_revision_fqdn
}
