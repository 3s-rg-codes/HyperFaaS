# HyperFaaS Functions

This directory contains different images of simple functions.
Currently, if you want to create a new function, just copy-paste one of the existing functions and modify it.

As there is no trivial way to build the containers, run/look at the justfile for instructions on how to build them.

# Go Functions

Every subfolder of the `go` directory is a separate function. You can generate the image by running `just build-function-go <function-name>`.

## How it works internally

The Go functions are part of the main HyperFaaS module (for our convenience when developing). This means that building the images is not trivial, however.
Every function needs to have a `main.go` file with the default main function. Everything else in the corresponding function directory will be copied as well during the build process.

If you want to understand how 