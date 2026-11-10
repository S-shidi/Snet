#!/bin/bash
# SNET 群晖 NAS 客户端更新脚本（legacy；优先用 scripts/update-nas.sh）
# 需要在群晖 DSM 的 任务计划 或 SSH 中以 root 权限运行
# 用法: SNET_VERSION=1.1.0 ./update-nas-synology.sh

set -e

echo "=== SNET 群晖 NAS 客户端更新 ==="
echo "时间: $(date)"
echo

# 配置
SNET_DIR="/volume1/@appdata/ContainerManager/all_shares/docker/snet"
BUILD_DIR="/tmp/snet-update"
DOCKER_COMPOSE="/var/packages/ContainerManager/target/usr/bin/docker-compose"
VERSION="${SNET_VERSION:-1.1.0}"
IMAGE="snet-client:v${VERSION}"

# 1. 检查新版本文件
if [ ! -f "/tmp/snetd.new" ]; then
    echo "错误: 未找到新版本文件 /tmp/snetd.new"
    echo "请先上传新版本文件"
    exit 1
fi

echo "✓ 新版本文件已就绪"
ls -lh /tmp/snetd.new

# 2. 创建构建目录
echo
echo "步骤 1: 创建镜像构建目录..."
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cp /tmp/snetd.new "$BUILD_DIR/snetd"
chmod +x "$BUILD_DIR/snetd"

cat > "$BUILD_DIR/Dockerfile" << 'DOCKERFILE'
FROM alpine:3.18
RUN apk add --no-cache ca-certificates iptables iproute2
COPY snetd /usr/local/bin/snetd
RUN chmod +x /usr/local/bin/snetd
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/snetd"]
DOCKERFILE

echo "✓ 构建目录已创建"

# 3. 停止旧容器
echo
echo "步骤 2: 停止旧容器..."
cd "$SNET_DIR"

if docker ps | grep -q "snet-client"; then
    echo "容器正在运行，尝试停止..."
    $DOCKER_COMPOSE down 2>&1 || {
        echo "docker-compose 失败，尝试直接停止容器..."
        docker stop snet-client 2>&1 || true
        docker rm snet-client 2>&1 || true
    }
    echo "✓ 容器已停止"
else
    echo "容器未运行"
fi

# 4. 备份旧镜像
echo
echo "步骤 3: 备份旧镜像..."
if docker images | grep -q "snet-client.*amd64"; then
    docker save snet-client:amd64 -o /tmp/snet-client-backup.tar 2>&1 || echo "警告: 备份失败（可能镜像不存在）"
    echo "✓ 旧镜像已备份到 /tmp/snet-client-backup.tar"
fi

# 5. 构建新镜像
echo
echo "步骤 4: 构建新镜像..."
cd "$BUILD_DIR"
docker build -t "$IMAGE" . 2>&1
docker tag "$IMAGE" snet-client:amd64 2>&1 || true
echo "✓ 新镜像已构建"

# 6. 更新 docker-compose.yml
echo
echo "步骤 5: 更新 docker-compose.yml..."
cp "$SNET_DIR/docker-compose.yml" "$SNET_DIR/docker-compose.yml.bak" 2>&1 || true

cat > "$SNET_DIR/docker-compose.yml" << 'EOF'
services:
  snet-client:
    image: __IMAGE__
    container_name: snet-client
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - ./data:/data
    environment:
      - SNET_SERVER=
      - SNET_BIND_CODE=
EOF

sed -i "s|__IMAGE__|$IMAGE|" "$SNET_DIR/docker-compose.yml"
echo "✓ 配置已更新"

# 7. 启动新容器
echo
echo "步骤 6: 启动新容器..."
cd "$SNET_DIR"
$DOCKER_COMPOSE up -d 2>&1 || {
    echo "docker-compose 失败，尝试直接启动..."
    docker run -d \
        --name snet-client \
        --restart unless-stopped \
        --network host \
        --cap-add NET_ADMIN \
        --device /dev/net/tun:/dev/net/tun \
        -v "$SNET_DIR/data:/data" \
        "$IMAGE"
}

sleep 3

# 8. 验证运行
echo
echo "步骤 7: 验证容器运行..."
if docker ps | grep -q "snet-client"; then
    echo "✓ 容器运行成功"
    docker ps | grep snet-client
else
    echo "✗ 容器启动失败"
    echo "查看日志:"
    docker logs snet-client 2>&1 | tail -20
    exit 1
fi

# 9. 清理
echo
echo "步骤 8: 清理临时文件..."
rm -rf "$BUILD_DIR"
echo "✓ 清理完成"

echo
echo "=== 更新成功 ==="
echo "版本: v${VERSION}"
echo
echo "验证命令:"
echo "  docker logs snet-client"
echo "  docker exec snet-client /usr/local/bin/snetd -version"