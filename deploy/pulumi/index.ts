// xtandard-image with Pulumi (docs/DEPLOY.md "Pulumi"). One program, three targets chosen by
// `pulumi config set target aws|gcp|k8s`. Secrets: `pulumi config set --secret signingKeys <hex>`
// and `signingSalts`. Nothing here runs without `pulumi up`; `bun run typecheck` checks it.
import * as pulumi from "@pulumi/pulumi";
import { deployAws } from "./aws";
import { deployGcp } from "./gcp";
import { deployK8s } from "./k8s";
import type { Settings } from "./settings";

const config = new pulumi.Config();

const settings: Settings = {
  image: config.get("image") ?? "ghcr.io/xantiagoma/xtandard-image:0.1.0-slim",
  allowedHosts: config.get("allowedHosts") ?? "images.example.com",
  minInstances: config.getNumber("minInstances") ?? 2,
  maxInstances: config.getNumber("maxInstances") ?? 20,
  signingKeys: config.requireSecret("signingKeys"),
  signingSalts: config.requireSecret("signingSalts"),
};

const target = config.get("target") ?? "aws";

function deploy(): pulumi.Output<string> {
  switch (target) {
    case "aws":
      return deployAws(settings);
    case "gcp":
      return deployGcp(settings);
    case "k8s":
      return deployK8s(settings);
    default:
      throw new Error(`unknown target ${target}: use aws, gcp or k8s`);
  }
}

export const url = deploy();
