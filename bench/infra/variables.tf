variable "region" {
  description = "AWS region for the reference environment."
  type        = string
  default     = "us-east-1"
}

variable "availability_zone" {
  description = "Single AZ for every node (the reference environment is same-AZ)."
  type        = string
  default     = "us-east-1a"
}

variable "key_name" {
  description = "Existing EC2 key pair for SSH."
  type        = string
}

variable "allowed_cidr" {
  description = "Your IP range (x.x.x.x/32) allowed to SSH and to reach the UI on :8080."
  type        = string
}

variable "image" {
  description = "Omni-Data-Bus image under test, pinned to the release candidate."
  type        = string
  default     = "ghcr.io/sahnianuj/omni-data-bus:1.0.0-rc1"
}

variable "couchbase_image" {
  description = "Couchbase Server image (8.x or 7.6)."
  type        = string
  default     = "couchbase/server:enterprise-8.0.3"
}

variable "worker_instance_type" {
  description = "Worker: 8 vCPU / 16 GB."
  type        = string
  default     = "c6i.2xlarge"
}

variable "source_instance_type" {
  description = "PostgreSQL 16: 8 vCPU."
  type        = string
  default     = "m6i.2xlarge"
}

variable "couchbase_instance_type" {
  description = "Each of the 3 Couchbase nodes: 8 vCPU / 32 GB."
  type        = string
  default     = "m6i.2xlarge"
}

variable "harness_instance_type" {
  description = "Runs odbbench (kept off the worker so it doesn't steal CPU)."
  type        = string
  default     = "c6i.large"
}

variable "sandbox_ram_mb" {
  description = "Per-node RAM quota of the target bucket: enough to hold the largest dataset."
  type        = number
  default     = 22528
}
