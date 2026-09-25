# Claude Code sandbox image.
#
# Deliberately has no sudo, no iptables and no network tooling: egress is
# enforced outside this container by the proxy sidecar (../proxy) and the
# internal Docker network that moat creates. Nothing running in here
# can loosen that.
FROM node:22-bookworm

ARG TZ=Europe/Oslo
ENV TZ="$TZ"

RUN apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates \
  curl \
  wget \
  git \
  gh \
  jq \
  less \
  procps \
  man-db \
  unzip \
  gnupg2 \
  fzf \
  zsh \
  ripgrep \
  fd-find \
  neovim \
  python3 \
  && apt-get clean && rm -rf /var/lib/apt/lists/* \
  && ln -s "$(command -v fdfind)" /usr/local/bin/fd

RUN mkdir -p /usr/local/share/npm-global && chown -R node:node /usr/local/share

# Persisted shell history (named volume mounted here by moat)
RUN mkdir -p /commandhistory && touch /commandhistory/.bash_history && chown -R node:node /commandhistory

RUN mkdir -p /workspace /home/node/.claude /home/node/.local/bin /home/node/.local/share && \
  chown -R node:node /workspace /home/node/.claude /home/node/.local

# PreToolUse hook that appends every tool call to ~/.claude/audit.log, and the
# status line that marks the session as sandboxed.
COPY audit-hook.sh /usr/local/bin/moat-audit-hook
COPY statusline.sh /usr/local/bin/moat-statusline
RUN chmod 0755 /usr/local/bin/moat-audit-hook /usr/local/bin/moat-statusline

WORKDIR /workspace
USER node

ENV NPM_CONFIG_PREFIX=/usr/local/share/npm-global
ENV PATH=$PATH:/usr/local/share/npm-global/bin:/home/node/.local/bin
ENV SHELL=/bin/zsh
ENV EDITOR=nvim
ENV VISUAL=nvim

ARG ZSH_IN_DOCKER_VERSION=1.2.0
RUN sh -c "$(wget -O- https://github.com/deluan/zsh-in-docker/releases/download/v${ZSH_IN_DOCKER_VERSION}/zsh-in-docker.sh)" -- \
  -p git \
  -p fzf \
  -a "source /usr/share/doc/fzf/examples/key-bindings.zsh" \
  -a "source /usr/share/doc/fzf/examples/completion.zsh" \
  -a "export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  -x

# Claude Code via the native installer. Auto-update is disabled at runtime;
# rebuild the image (moat build) to update.
ARG CLAUDE_CODE_VERSION=latest
RUN curl -fsSL https://claude.ai/install.sh | bash -s "${CLAUDE_CODE_VERSION}"
