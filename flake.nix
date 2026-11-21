{
  description = "tgread: Minimal, compile-time read-only Telegram MCP server and CLI in Go";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      version = "0.1.0";
      supportedSystems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forEachSupportedSystem = f: nixpkgs.lib.genAttrs supportedSystems (system: f {
        pkgs = import nixpkgs { inherit system; };
        inherit system;
      });
    in
    {
      devShells = forEachSupportedSystem ({ pkgs, ... }: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            go-tools
            go-task
            git
          ];
          shellHook = ''
            if [ -d .git ] && [ -d .githooks ]; then
              git config core.hooksPath .githooks
            fi
          '';
        };
      });

      packages = forEachSupportedSystem ({ pkgs, ... }:
        let
          tgreadPkg = pkgs.buildGoModule {
            pname = "tgread";
            inherit version;
            src = ./.;
            vendorHash = "sha256-BvuHOD8/T1lnyzTPk4k5sACux8tQZ33NAkZnBOA/zwI=";
            env = {
              CGO_ENABLED = "0";
            };
            subPackages = [ "cmd/tgread" ];
            ldflags = [
              "-s"
              "-w"
              "-X main.appVersion=${version}"
            ];
          };
        in
        {
          default = tgreadPkg;
          tgread = tgreadPkg;
        });

      checks = forEachSupportedSystem ({ system, ... }: {
        package = self.packages.${system}.default;
      });

      overlays.default = final: prev: {
        tgread = self.packages.${prev.stdenv.hostPlatform.system}.tgread;
      };
    };
}
