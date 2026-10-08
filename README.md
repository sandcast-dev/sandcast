# sandcast

sandcast turns a project directory into a sandbox image.

Sand casting pours metal into a pre-formed mold. sandcast pours a project and its developer's tooling into a pre-configured base image. A guided capture examines the project and the host and writes a manifest. A build probes the chosen base image, adds only what is missing, and produces the OpenShell provider profiles adapted to that image, so a sandbox started from it gets the right network policy at runtime.

## Status

Specification stage. Nothing is implemented yet.

- Feature specification: [specs/001-sandcast-standalone-build/spec.md](specs/001-sandcast-standalone-build/spec.md)
- Design notes and research: [brainstorm/01-sandcast-standalone-build.md](brainstorm/01-sandcast-standalone-build.md)

## Origin

The capture-to-manifest-to-image flow was built inside [cc-deck](https://github.com/cc-deck/cc-deck). sandcast extracts it into a standalone tool for any project and any supported agent harness, drops the home-grown policy catalog in favor of upstream [OpenShell](https://github.com/NVIDIA/OpenShell) provider profiles, and uses [mise](https://mise.jdx.dev) as the installer.

## License

Apache License 2.0. See [LICENSE](LICENSE).
