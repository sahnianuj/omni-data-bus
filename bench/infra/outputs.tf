output "harness_ssh" {
  description = "Log in to the harness host (run odbbench from here)."
  value       = "ssh ubuntu@${aws_instance.harness.public_ip}"
}

output "ui" {
  description = "Omni-Data-Bus UI (reachable from allowed_cidr)."
  value       = "http://${aws_instance.worker.public_ip}:8080"
}

output "odbbench_command" {
  description = "The release benchmark, to run on the harness host from the bench/ directory."
  sensitive   = true
  value = join(" ", [
    "PGPASSWORD=${random_password.postgres.result}",
    "go run ./cmd/odbbench --dataset standard --gate release",
    "--api http://${local.worker_ip}:8080",
    "--metrics http://${local.worker_ip}:9090/metrics",
    "--pprof http://${local.worker_ip}:6060",
    "--psql 'psql -h ${local.source_ip} -U bench -d bench'",
    "--source-host ${local.source_ip} --source-db bench --source-user bench --source-password ${random_password.postgres.result}",
    "--target couchbase://${join(",", local.couchbase_ips)} --target-user Administrator --target-password ${random_password.couchbase.result}",
    "--env 'worker=${var.worker_instance_type} (8 vCPU / 16 GB)'",
    "--env 'source=PostgreSQL 16 on ${var.source_instance_type}'",
    "--env 'couchbase=3 × ${var.couchbase_instance_type}, ${var.couchbase_image}'",
    "--env 'region=${var.region} / ${var.availability_zone}'",
  ])
}
