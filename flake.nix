{
  description = "tgread: Minimal, compile-time read-only Telegram MCP server and CLI in Go";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
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
        };
      });

      packages = forEachSupportedSystem ({ pkgs, ... }:
        let
          tgreadPkg = pkgs.buildGoModule {
            pname = "tgread";
            version = "0.1.0";
            src = ./.;
            vendorHash = "sha256-BvuHOD8/T1lnyzTPk4k5sACux8tQZ33NAkZnBOA/zwI=";
            env = {
              CGO_ENABLED = "0";
            };
            subPackages = [ "cmd/tgread" ];
            ldflags = [ "-s" "-w" ];
          };
        in
        {
          default = tgreadPkg;
          tgread = tgreadPkg;
        });

      overlays.default = final: prev: {
        tgread = self.packages.${prev.stdenv.hostPlatform.system}.tgread;
      };
    };
}
