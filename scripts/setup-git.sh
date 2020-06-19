#!/bin/sh

git config --global url."git@github.com:".insteadOf "https://github.com/"

mkdir -p ~/.ssh

cat >~/.ssh/config <<EOF
Host github.com
  IdentityFile ~/.ssh/deploy.id_rsa
EOF
