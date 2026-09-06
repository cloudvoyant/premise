const std = @import("std");
const lib = @import("mise_lib_template");

pub fn main() void {
    const result = lib.startsWith("hello", "he");
    std.debug.print("Hello from mise-lib-template!\n", .{});
    std.debug.print("startsWith(\"hello\", \"he\"): {}\n", .{result});
}
