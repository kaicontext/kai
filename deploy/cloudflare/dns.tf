# Cloudflare DNS for Kailab
# Points to nginx ingress: 34.30.21.207

terraform {
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 4.0"
    }
  }
}

# Configure via environment variables:
# export CLOUDFLARE_API_TOKEN="your-api-token"
provider "cloudflare" {}

variable "zone_id" {
  description = "Cloudflare Zone ID"
  type        = string
}

variable "domain" {
  description = "Base domain (e.g., example.com)"
  type        = string
}

variable "ingress_ip" {
  description = "Nginx ingress IP address"
  type        = string
  default     = "34.30.21.207"
}

variable "ssh_ip" {
  description = "SSH LoadBalancer IP address"
  type        = string
  default     = "34.9.252.213"
}

# Root domain
resource "cloudflare_record" "root" {
  zone_id = var.zone_id
  name    = "@"
  content = var.ingress_ip
  type    = "A"
  ttl     = 1  # Auto (proxied)
  proxied = true
}

# Wildcard for multi-tenant (optional)
# e.g., org1.kailayer.com, org2.kailayer.com
resource "cloudflare_record" "wildcard" {
  zone_id = var.zone_id
  name    = "*"
  content = var.ingress_ip
  type    = "A"
  ttl     = 1
  proxied = true
}

# Git SSH endpoint (not proxied - SSH can't go through Cloudflare)
resource "cloudflare_record" "git" {
  zone_id = var.zone_id
  name    = "git"
  content = var.ssh_ip
  type    = "A"
  ttl     = 300
  proxied = false
}

output "kailab_url" {
  value = "https://${var.domain}"
}

output "git_ssh_url" {
  value = "git@git.${var.domain}"
}
