FROM golang:1.26-bookworm

ARG DEBIAN_FRONTEND=noninteractive
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=1000

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bash \
		ca-certificates \
		curl \
		flatbuffers-compiler \
		git \
		jq \
		make \
		nodejs \
		npm \
		unzip \
	&& rm -rf /var/lib/apt/lists/*

RUN groupadd --gid "${USER_GID}" "${USERNAME}" \
	&& useradd --uid "${USER_UID}" --gid "${USER_GID}" -m "${USERNAME}" \
	&& mkdir -p /workspaces \
	&& chown -R "${USERNAME}:${USERNAME}" /workspaces

USER ${USERNAME}
ENV GOPATH=/home/${USERNAME}/go
ENV PATH=${GOPATH}/bin:${PATH}
RUN go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

 

