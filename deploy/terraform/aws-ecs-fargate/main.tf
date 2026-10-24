# AWS ECS on Fargate behind an ALB, with target-tracking autoscaling and an optional CloudFront
# distribution in front (docs/DEPLOY.md "Terraform: ECS on Fargate").

locals {
  container = "xtandard-image"
  env = merge({
    XI_SOURCES                 = "web"
    XI_SOURCES_WEB_TYPE        = "http"
    XI_SOURCES_WEB_ALLOW       = var.allowed_hosts
    XI_CACHE_RESULT_STORES     = "memory"
    XI_SERVER_SHUTDOWN_DELAY   = "5s"
    XI_SERVER_SHUTDOWN_TIMEOUT = "30s"
  }, var.extra_env)
}

# --- Secrets (values set out of band: aws secretsmanager put-secret-value) -----------------

resource "aws_secretsmanager_secret" "xi" {
  for_each = toset(["xi-signing-keys", "xi-signing-salts"])
  name     = "${var.name}/${each.key}"
}

# --- IAM ------------------------------------------------------------------------------------

data "aws_iam_policy_document" "ecs_tasks" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "execution" {
  name               = "${var.name}-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks.json
}

resource "aws_iam_role_policy_attachment" "execution" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_iam_policy_document" "secrets" {
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = concat([for s in aws_secretsmanager_secret.xi : s.arn], var.registry_credentials_arn == "" ? [] : [var.registry_credentials_arn])
  }
}

resource "aws_iam_role_policy" "execution_secrets" {
  name   = "secrets"
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.secrets.json
}

# The task role is what an s3 source uses (no keys in the environment).
resource "aws_iam_role" "task" {
  name               = "${var.name}-task"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks.json
}

# --- Networking -----------------------------------------------------------------------------

resource "aws_security_group" "alb" {
  name   = "${var.name}-alb"
  vpc_id = var.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "alb_http" {
  security_group_id = aws_security_group.alb.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = var.certificate_arn == "" ? 80 : 443
  to_port           = var.certificate_arn == "" ? 80 : 443
}

resource "aws_vpc_security_group_egress_rule" "alb_to_tasks" {
  security_group_id            = aws_security_group.alb.id
  referenced_security_group_id = aws_security_group.tasks.id
  ip_protocol                  = "tcp"
  from_port                    = 8080
  to_port                      = 8080
}

resource "aws_security_group" "tasks" {
  name   = "${var.name}-tasks"
  vpc_id = var.vpc_id
}

# Only the ALB reaches 8080; the admin listener (127.0.0.1:9090 by default) is not exposed.
resource "aws_vpc_security_group_ingress_rule" "tasks_from_alb" {
  security_group_id            = aws_security_group.tasks.id
  referenced_security_group_id = aws_security_group.alb.id
  ip_protocol                  = "tcp"
  from_port                    = 8080
  to_port                      = 8080
}

resource "aws_vpc_security_group_egress_rule" "tasks_out" {
  security_group_id = aws_security_group.tasks.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# --- Load balancer --------------------------------------------------------------------------

resource "aws_lb" "xi" {
  name               = var.name
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.public_subnet_ids
  # Longer than XI_SERVER_REQUEST_TIMEOUT (20s).
  idle_timeout               = 60
  drop_invalid_header_fields = true
}

resource "aws_lb_target_group" "xi" {
  name        = var.name
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = var.vpc_id
  # Draining: XI_SERVER_SHUTDOWN_DELAY + the longest request.
  deregistration_delay = 30

  health_check {
    path                = "/readyz"
    matcher             = "200"
    interval            = 10
    timeout             = 3
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_listener" "xi" {
  load_balancer_arn = aws_lb.xi.arn
  port              = var.certificate_arn == "" ? 80 : 443
  protocol          = var.certificate_arn == "" ? "HTTP" : "HTTPS"
  certificate_arn   = var.certificate_arn == "" ? null : var.certificate_arn
  ssl_policy        = var.certificate_arn == "" ? null : "ELBSecurityPolicy-TLS13-1-2-2021-06"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.xi.arn
  }
}

# --- ECS ------------------------------------------------------------------------------------

resource "aws_ecs_cluster" "xi" {
  name = var.name
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_cloudwatch_log_group" "xi" {
  name              = "/ecs/${var.name}"
  retention_in_days = 30
}

resource "aws_ecs_task_definition" "xi" {
  family                   = var.name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  # Fargate has no tmpfs: /tmp is a bind mount on the task's ephemeral storage.
  volume {
    name = "tmp"
  }

  container_definitions = jsonencode([{
    name                   = local.container
    image                  = var.image
    essential              = true
    readonlyRootFilesystem = true
    user                   = "65532:65532"
    repositoryCredentials  = var.registry_credentials_arn == "" ? null : { credentialsParameter = var.registry_credentials_arn }
    portMappings           = [{ name = "http", containerPort = 8080, protocol = "tcp", appProtocol = "http" }]
    environment            = [for k, v in local.env : { name = k, value = v }]
    secrets = [
      { name = "XI_SIGNING_KEYS", valueFrom = aws_secretsmanager_secret.xi["xi-signing-keys"].arn },
      { name = "XI_SIGNING_SALTS", valueFrom = aws_secretsmanager_secret.xi["xi-signing-salts"].arn },
    ]
    mountPoints = [{ sourceVolume = "tmp", containerPath = "/tmp", readOnly = false }]
    healthCheck = {
      command     = ["CMD", "/usr/local/bin/xtandard-image", "health"]
      interval    = 10
      timeout     = 3
      retries     = 3
      startPeriod = 10
    }
    # SIGTERM → /readyz 503 → 5 s → drain up to 30 s (Fargate allows up to 120 s).
    stopTimeout     = 40
    linuxParameters = { capabilities = { drop = ["ALL"] }, initProcessEnabled = true }
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.xi.name
        awslogs-region        = var.region
        awslogs-stream-prefix = "xi"
      }
    }
  }])
}

resource "aws_ecs_service" "xi" {
  name                              = var.name
  cluster                           = aws_ecs_cluster.xi.id
  task_definition                   = aws_ecs_task_definition.xi.arn
  desired_count                     = var.min_tasks
  launch_type                       = "FARGATE"
  health_check_grace_period_seconds = 15

  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.tasks.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.xi.arn
    container_name   = local.container
    container_port   = 8080
  }

  # Autoscaling owns the count after creation.
  lifecycle {
    ignore_changes = [desired_count]
  }

  depends_on = [aws_lb_listener.xi]
}

# --- Autoscaling ----------------------------------------------------------------------------

resource "aws_appautoscaling_target" "xi" {
  service_namespace  = "ecs"
  resource_id        = "service/${aws_ecs_cluster.xi.name}/${aws_ecs_service.xi.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  min_capacity       = var.min_tasks
  max_capacity       = var.max_tasks
}

# CPU is the primary signal: workers equal vCPUs, so CPU tracks how busy they are.
resource "aws_appautoscaling_policy" "cpu" {
  name               = "${var.name}-cpu"
  policy_type        = "TargetTrackingScaling"
  service_namespace  = aws_appautoscaling_target.xi.service_namespace
  resource_id        = aws_appautoscaling_target.xi.resource_id
  scalable_dimension = aws_appautoscaling_target.xi.scalable_dimension

  target_tracking_scaling_policy_configuration {
    target_value       = var.cpu_target
    scale_out_cooldown = 60
    scale_in_cooldown  = 300
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
  }
}

# Requests per task as a second signal (the larger desired count of the two policies wins).
resource "aws_appautoscaling_policy" "requests" {
  count              = var.requests_per_target > 0 ? 1 : 0
  name               = "${var.name}-requests"
  policy_type        = "TargetTrackingScaling"
  service_namespace  = aws_appautoscaling_target.xi.service_namespace
  resource_id        = aws_appautoscaling_target.xi.resource_id
  scalable_dimension = aws_appautoscaling_target.xi.scalable_dimension

  target_tracking_scaling_policy_configuration {
    target_value       = var.requests_per_target
    scale_out_cooldown = 60
    scale_in_cooldown  = 300
    predefined_metric_specification {
      predefined_metric_type = "ALBRequestCountPerTarget"
      resource_label         = "${aws_lb.xi.arn_suffix}/${aws_lb_target_group.xi.arn_suffix}"
    }
  }
}
