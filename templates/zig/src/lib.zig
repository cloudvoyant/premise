//! mise_lib_template — generated from mise-zig-template.

const std = @import("std");

/// Returns true if haystack starts with needle.
pub fn startsWith(haystack: []const u8, needle: []const u8) bool {
    if (needle.len > haystack.len) return false;
    return std.mem.eql(u8, haystack[0..needle.len], needle);
}

/// Returns true if haystack ends with needle.
pub fn endsWith(haystack: []const u8, needle: []const u8) bool {
    if (needle.len > haystack.len) return false;
    return std.mem.eql(u8, haystack[haystack.len - needle.len ..], needle);
}

test "startsWith" {
    try std.testing.expect(startsWith("hello world", "hello"));
    try std.testing.expect(!startsWith("hello", "world"));
}

test "endsWith" {
    try std.testing.expect(endsWith("hello world", "world"));
    try std.testing.expect(!endsWith("hello", "world"));
}
