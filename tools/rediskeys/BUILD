load("@io_bazel_rules_go//go:def.bzl", "go_binary", "go_library")

go_library(
    name = "rediskeys_lib",
    srcs = ["rediskeys.go"],
    importpath = "github.com/buildbuddy-io/buildbuddy/tools/rediskeys",
    visibility = ["//visibility:private"],
    deps = [
        "//server/util/log",
        "@com_github_go_redis_redis_v8//:redis",
    ],
)

go_binary(
    name = "rediskeys",
    embed = [":rediskeys_lib"],
    visibility = ["//visibility:public"],
)
