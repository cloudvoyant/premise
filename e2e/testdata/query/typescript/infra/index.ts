import * as pulumi from "@pulumi/pulumi";
import * as random from "@pulumi/random";

const id = new random.RandomId("duplicate", { byteLength: 8 });
new random.RandomString("duplicate", { length: 8 });

export const publicId = id.hex;
export const branchMarker = pulumi.getStack();
export const privateValue = pulumi.secret("query-fixture-secret");
