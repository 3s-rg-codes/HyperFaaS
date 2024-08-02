# GRPC

It should be possible to build HyperFaaS without touching these files, as we commit the generated files.

## Installation

If you want to compile the proto files yourself, you need to install protoc. In Ubuntu, the commands are:

```bash
sudo apt install -y protobuf-compiler
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2
export PATH="$PATH:$(go env GOPATH)/bin"
```

(You will need to run the export command every time you open a new terminal, or add it to your `.bashrc` or `.zshrc` file.)

On MacOS, you can use Homebrew:

```bash
brew install protobuf protoc-gen-go protoc-gen-go-grpc
```

Then, you can compile the proto files with the following command: `just proto`
