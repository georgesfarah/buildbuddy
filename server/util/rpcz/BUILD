load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "rpcz",
    srcs = ["rpcz.go"],
    importpath = "github.com/buildbuddy-io/buildbuddy/server/util/rpcz",
    visibility = ["//visibility:public"],
    deps = [
        "@com_github_google_uuid//:uuid",
        "@org_golang_google_grpc//peer",
        "@org_golang_google_grpc//stats",
    ],
)
