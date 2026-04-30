FROM golang:1.26-bookworm AS go-tools
ENV CGO_ENABLED=1
RUN go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

FROM node:22-bookworm-slim AS app

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ARG DEBIAN_FRONTEND=noninteractive
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=1000

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bash \
		build-essential \
		ca-certificates \
		curl \
		git \
		jq \
		make \
		pkg-config \
		unzip \
	&& rm -rf /var/lib/apt/lists/*

COPY scripts/install-flatc.sh /tmp/install-flatc.sh
RUN bash /tmp/install-flatc.sh /usr/local/bin \
	&& rm -f /tmp/install-flatc.sh

COPY --from=go-tools /usr/local/go /usr/local/go
COPY --from=go-tools /go/bin/sqlc /usr/local/bin/sqlc

ENV CGO_ENABLED=1 \
	GOPATH=/home/${USERNAME}/go \
	PATH=/usr/local/go/bin:/home/${USERNAME}/go/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN ln -s /usr/local/go/bin/go /usr/local/bin/go \
	&& ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt

RUN set -eu; \
	if ! getent group "${USER_GID}" >/dev/null 2>&1; then \
		groupadd --gid "${USER_GID}" "${USERNAME}"; \
	fi; \
	if id -u "${USER_UID}" >/dev/null 2>&1; then \
		existing_user="$(id -un "${USER_UID}")"; \
		if [ "${existing_user}" != "${USERNAME}" ]; then \
			usermod -l "${USERNAME}" -d "/home/${USERNAME}" -m "${existing_user}"; \
		fi; \
	elif ! id -u "${USERNAME}" >/dev/null 2>&1; then \
		useradd --uid "${USER_UID}" --gid "${USER_GID}" --create-home --shell /bin/bash --non-unique "${USERNAME}"; \
	else \
		usermod -u "${USER_UID}" "${USERNAME}" --non-unique; \
	fi; \
	usermod -g "${USER_GID}" "${USERNAME}" || true; \
	mkdir -p /workspaces /home/${USERNAME}/go; \
	chown -R "${USERNAME}:${USER_GID}" /workspaces /home/${USERNAME} /home/${USERNAME}/go

USER ${USERNAME}
WORKDIR /workspaces/${USERNAME}
