load("@io_bazel_rules_go//go:def.bzl", "go_library", "go_test")

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

go_test(
    name = "rpcz_test",
    srcs = ["rpcz_test.go"],
    deps = [
        ":rpcz",
        "//server/testutil/testport",
        "@com_github_stretchr_testify//require",
        "@org_golang_google_genproto_googleapis_bytestream//:bytestream",
        "@org_golang_google_grpc//:grpc",
        "@org_golang_google_grpc//credentials/insecure",
    ],
)
