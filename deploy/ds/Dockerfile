# Pandora 战斗/大厅 DS（Linux Dedicated Server）镜像。
# Agones Fleet 调度此镜像；Agones 会向同一 Pod 注入 sidecar（提供 SDK，端口经
# AGONES_SDK_HTTP_PORT/AGONES_SDK_GRPC_PORT 注入），DS 进程内由
# UPandoraAgonesSubsystem 走本地 HTTP 完成 Ready/Health/Shutdown。本镜像本身不需打包 Agones SDK。
#
# 前置：先在客户端仓库用 build-linux-ds.ps1（Windows + UE 跨平台工具链）打出 Linux 服务器，
# 把归档产物中的 LinuxServer/ 目录拷到本 Dockerfile 同级的 ./stage/LinuxServer 下，再 docker build。
#
#   docker build -f deploy/ds/Dockerfile -t pandora/battle-ds:dev deploy/ds
#
ARG BASE_IMAGE=ubuntu:22.04
FROM ${BASE_IMAGE}

# UE 无头专用服务器的最小运行期依赖。
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates libatomic1 tzdata \
    && rm -rf /var/lib/apt/lists/*

# 非 root 运行（Agones / K8s 安全基线）。
RUN useradd -m -u 10001 pandora
WORKDIR /home/pandora/server

# 拷入已打包的 Linux 服务器产物（由 build-linux-ds.ps1 归档后放到 stage/）。
COPY --chown=pandora:pandora stage/LinuxServer/ /home/pandora/server/
COPY --chown=pandora:pandora entrypoint.sh /home/pandora/entrypoint.sh
RUN chmod +x /home/pandora/entrypoint.sh \
    && find /home/pandora/server -name "*.sh" -exec chmod +x {} \; \
    && (find /home/pandora/server -path "*/Binaries/Linux/PandoraServer" -exec chmod +x {} \; || true)

USER pandora

# Agones Fleet 里 containerPort=7777/UDP（portPolicy: Dynamic 由 Agones 映射主机端口）。
EXPOSE 7777/udp

# 关卡 / 端口可被 K8s env 覆盖（见 entrypoint.sh）。
ENV PANDORA_DS_MAP="/Game/Entry/Level/Lvl_Server_Entry" \
    PANDORA_DS_PORT="7777"

ENTRYPOINT ["/home/pandora/entrypoint.sh"]
