{
  description = "protonpass-server dev environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        # vscode-go wants its tools built with the same Go as the toolchain
        withGo127 = p: p.override { buildGoModule = pkgs.buildGo127Module; };
      in {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go_1_27 # toolchain
            gopls   # LSP
            proton-pass-cli # wrapped by the server
            kubernetes-helm # chart
            kubectl         # deploy and inspect
            kind            # local cluster
            helm-docs       # chart README from values.yaml
          ] ++ map withGo127 (with pkgs; [ delve gotools go-tools gomodifytags impl gotests ]);
        };
      });
}
