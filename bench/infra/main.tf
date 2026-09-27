# Reference environment for the OC-034 release benchmark (see ../README.md). Everything sits in one
# subnet of one AZ inside a cluster placement group, so the network isn't what's being measured.

data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical
  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }
}

resource "aws_vpc" "bench" {
  cidr_block           = "10.60.0.0/16"
  enable_dns_hostnames = true
  tags                 = { Name = "odb-bench" }
}

resource "aws_subnet" "bench" {
  vpc_id                  = aws_vpc.bench.id
  cidr_block              = "10.60.1.0/24"
  availability_zone       = var.availability_zone
  map_public_ip_on_launch = true
  tags                    = { Name = "odb-bench" }
}

resource "aws_internet_gateway" "bench" {
  vpc_id = aws_vpc.bench.id
}

resource "aws_route_table" "bench" {
  vpc_id = aws_vpc.bench.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.bench.id
  }
}

resource "aws_route_table_association" "bench" {
  subnet_id      = aws_subnet.bench.id
  route_table_id = aws_route_table.bench.id
}

resource "aws_security_group" "bench" {
  name        = "odb-bench"
  description = "Benchmark nodes: open to each other; SSH and the UI from allowed_cidr only"
  vpc_id      = aws_vpc.bench.id

  ingress {
    description = "Between benchmark nodes"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    self        = true
  }
  ingress {
    description = "SSH"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.allowed_cidr]
  }
  ingress {
    description = "Omni-Data-Bus UI and API"
    from_port   = 8080
    to_port     = 8080
    protocol    = "tcp"
    cidr_blocks = [var.allowed_cidr]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_placement_group" "bench" {
  name     = "odb-bench"
  strategy = "cluster"
}

resource "random_password" "couchbase" {
  length  = 24
  special = false
}

resource "random_password" "postgres" {
  length  = 24
  special = false
}

resource "random_bytes" "master_key" {
  length = 32
}

locals {
  couchbase_ips = [for i in range(3) : cidrhost(aws_subnet.bench.cidr_block, 10 + i)]
  source_ip     = cidrhost(aws_subnet.bench.cidr_block, 20)
  worker_ip     = cidrhost(aws_subnet.bench.cidr_block, 30)
  harness_ip    = cidrhost(aws_subnet.bench.cidr_block, 40)
  common = {
    ami                    = data.aws_ami.ubuntu.id
    subnet_id              = aws_subnet.bench.id
    key_name               = var.key_name
    vpc_security_group_ids = [aws_security_group.bench.id]
  }
}

resource "aws_instance" "couchbase" {
  count                  = 3
  ami                    = local.common.ami
  instance_type          = var.couchbase_instance_type
  subnet_id              = local.common.subnet_id
  private_ip             = local.couchbase_ips[count.index]
  key_name               = local.common.key_name
  vpc_security_group_ids = local.common.vpc_security_group_ids
  placement_group        = aws_placement_group.bench.id
  root_block_device {
    volume_size = 200
    volume_type = "gp3"
    iops        = 6000
    throughput  = 400
  }
  user_data = templatefile("${path.module}/scripts/couchbase.sh.tftpl", {
    image          = var.couchbase_image
    index          = count.index
    nodes          = local.couchbase_ips
    password       = random_password.couchbase.result
    sandbox_ram_mb = var.sandbox_ram_mb
  })
  tags = { Name = "odb-bench-couchbase-${count.index}" }
}

resource "aws_instance" "source" {
  ami                    = local.common.ami
  instance_type          = var.source_instance_type
  subnet_id              = local.common.subnet_id
  private_ip             = local.source_ip
  key_name               = local.common.key_name
  vpc_security_group_ids = local.common.vpc_security_group_ids
  placement_group        = aws_placement_group.bench.id
  root_block_device {
    volume_size = 300
    volume_type = "gp3"
    iops        = 6000
    throughput  = 400
  }
  user_data = templatefile("${path.module}/scripts/source.sh.tftpl", {
    password = random_password.postgres.result
  })
  tags = { Name = "odb-bench-source" }
}

resource "aws_instance" "worker" {
  ami                    = local.common.ami
  instance_type          = var.worker_instance_type
  subnet_id              = local.common.subnet_id
  private_ip             = local.worker_ip
  key_name               = local.common.key_name
  vpc_security_group_ids = local.common.vpc_security_group_ids
  placement_group        = aws_placement_group.bench.id
  user_data = templatefile("${path.module}/scripts/worker.sh.tftpl", {
    image      = var.image
    nodes      = local.couchbase_ips
    password   = random_password.couchbase.result
    master_key = random_bytes.master_key.base64
    public_url = "http://${local.worker_ip}:8080"
  })
  tags       = { Name = "odb-bench-worker" }
  depends_on = [aws_instance.couchbase]
}

resource "aws_instance" "harness" {
  ami                    = local.common.ami
  instance_type          = var.harness_instance_type
  subnet_id              = local.common.subnet_id
  private_ip             = local.harness_ip
  key_name               = local.common.key_name
  vpc_security_group_ids = local.common.vpc_security_group_ids
  placement_group        = aws_placement_group.bench.id
  user_data              = file("${path.module}/scripts/harness.sh")
  tags                   = { Name = "odb-bench-harness" }
}
