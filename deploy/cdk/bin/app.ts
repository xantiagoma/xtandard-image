#!/usr/bin/env node
import { App } from "aws-cdk-lib";
import { XtandardImageStack } from "../lib/xtandard-image-stack";

const app = new App();
// Environment-agnostic, so `cdk synth` works without credentials; set env for lookups.
new XtandardImageStack(app, "XtandardImage", {
  image: app.node.tryGetContext("image") as string | undefined,
  allowedHosts: app.node.tryGetContext("allowedHosts") as string | undefined,
});
