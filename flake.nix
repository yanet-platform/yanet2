{
  description = "YANET2 development shell";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
  inputs.toolpkgs.url = "github:NixOS/nixpkgs/ac6b2166e7a9";

  outputs = { nixpkgs, toolpkgs, ... }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
    in {
      devShells = nixpkgs.lib.genAttrs systems (system:
        let
          pkgs = import nixpkgs { inherit system; };
          tools = import toolpkgs { inherit system; };
          python = pkgs.python3.withPackages (ps: [ ps.pyelftools ]);
        in {
          default = pkgs.mkShell {
            packages = with pkgs; [
              bison
              buf
              ccache
              cdrtools
              cmake
              flex
              gcc
              git
              gh
              gnumake
              tools.go
              tools.golangci-lint
              gopls
              jq
              meson
              ninja
              nodejs_22
              pkg-config
              protobuf
              tools.protoc-gen-go
              tools.protoc-gen-go-grpc
              python
              qemu
              shellcheck
              typescript-language-server
              wget
              llvmPackages_19.clang
              llvmPackages_19.clang-tools
            ];

            buildInputs = with pkgs; [
              libpcap
              libyaml
              numactl
              rdma-core
            ];

            # Some OOM tests intentionally compile with -O0 and -Werror.
            hardeningDisable = [ "fortify" ];
            LIBCLANG_PATH = "${pkgs.llvmPackages_19.libclang.lib}/lib";
            LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib ];
            PYTHONPATH = "${python}/${python.sitePackages}";
            shellHook = ''export CC=gcc CXX=g++'';
          };
        });
    };
}
