# Reference zsh configuration for the sandcast capture fixture.
# It mirrors a typical developer setup with host-specific pieces that the
# capture skill must strip (antidote, Homebrew paths) and tools it must add
# as components (starship, fzf, lsd).

export PATH="/opt/homebrew/bin:$HOME/.local/bin:$PATH"

# Plugin manager (host-only, stripped by capture).
source /opt/homebrew/opt/antidote/share/antidote/antidote.zsh
antidote load

# Completion styling; the curated configuration needs compinit for this.
zstyle ':completion:*' menu select
zstyle ':completion:*' matcher-list 'm:{a-z}={A-Z}'

# Prompt.
eval "$(starship init zsh)"

# Fuzzy finder key bindings and completion.
source <(fzf --zsh)

# Listing.
alias ls=lsd
alias ll='lsd -l'

setopt HIST_IGNORE_DUPS SHARE_HISTORY
HISTSIZE=10000
SAVEHIST=10000
