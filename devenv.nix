# devenv.nix (put this in the repo root)
{ pkgs, ... }:
{
  # CLI tools available in the shell
  packages = [ pkgs.git pkgs.ripgrep ];

  # Pin the Go toolchain used inside the dev shell
  languages.go = {
    enable = true;
    package = pkgs.go_1_24;   # if this doesn't exist in your channel, use pkgs.go_1_23 or pkgs.go
  };

  # Example script: run your agent like `devenv run agent "prompt"`
  scripts.agent.exec = ''go run . "$@"'';

  # A friendly banner + quick versions when you enter the shell
  env.GREET = "devenv";
  enterShell = ''
    echo "hello from $GREET"
    go version
    git --version
  '';

  # Optional test hook
  enterTest = ''
    echo "Running tests"
    git --version | grep --color=auto "${pkgs.git.version}"
  '';
}
