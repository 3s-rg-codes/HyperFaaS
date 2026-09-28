fn main() {
    tonic_build::compile_protos("proto/ready.proto").unwrap();
}
