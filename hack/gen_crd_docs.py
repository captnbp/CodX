#!/usr/bin/env python3
"""Generate a markdown reference document from the Profile CRD OpenAPI schema.

Usage:
    python3 hack/gen_crd_docs.py
    make docs-crd
"""
import os
import yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CRD_PATH = os.path.join(ROOT, "charts/codx/crds/codx.io_profiles.yaml")
OUT_PATH = os.path.join(ROOT, "docs/profile-crd.md")

# Fields whose schema embeds upstream Kubernetes core/corev1/metav1 types.
# Instead of expanding thousands of lines, we summarize and link to the K8s API docs.
K8S_TYPE_MAP = {
    "env": ("EnvVar", "corev1",
            "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#envvar-v1-core"),
    "volumes": ("Volume", "corev1",
                "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#volume-v1-core"),
    "volumeMounts": ("VolumeMount", "corev1",
                     "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#volumemount-v1-core"),
    "sidecars": ("Container", "corev1",
                 "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#container-v1-core"),
    "initContainers": ("Container", "corev1",
                       "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#container-v1-core"),
    "securityContext": ("PodSecurityContext", "corev1",
                        "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#podsecuritycontext-v1-core"),
    "resources": ("ResourceRequirements", "corev1",
                  "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#resourcerequirements-v1-core"),
    "conditions": ("Condition", "metav1",
                   "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta"),
}

# CodX-native object types whose nested fields we document in full.
CODEX_OBJECT_TYPES = {
    "podSpec": "ProfilePodSpec",
    "pvc": "ProfilePVC",
}

# Top-level spec fields we recurse into; everything else is a leaf.
TOP_LEVEL_OBJECTS = {"podSpec", "pvc"}


def clean_desc(desc):
    """Collapse multi-line YAML descriptions into a single trimmed string."""
    if not desc:
        return ""
    return " ".join(str(desc).split())


def field_type(prop):
    t = prop.get("type", "")
    if t == "array":
        items = prop.get("items", {})
        it = items.get("type", "object")
        return f"[]{it}"
    if t == "object" and "additionalProperties" in prop:
        ap = prop["additionalProperties"]
        ap_type = ap.get("type", "object") if isinstance(ap, dict) else "string"
        return f"map[string]{ap_type}"
    return t or "object"


def render_table(rows):
    """rows: list of (name, type, required, default, description)."""
    out = ["| Field | Type | Required | Default | Description |",
           "|-------|------|----------|---------|-------------|"]
    for name, ftype, required, default, desc in rows:
        req = "Yes" if required else "No"
        if default is None or default == "":
            dflt = "-"
        elif isinstance(default, bool):
            dflt = "true" if default else "false"
        else:
            dflt = default
        out.append(f"| `{name}` | `{ftype}` | {req} | `{dflt}` | {desc} |")
    return "\n".join(out)


def doc_object(name, schema, level=3, anchor_type=None):
    """Document a CodX-native object type: heading + fields table + recursion."""
    lines = []
    props = schema.get("properties", {})
    required = schema.get("required", [])
    title = anchor_type or name
    lines.append(f"{'#' * level} {title}")
    lines.append("")
    desc = clean_desc(schema.get("description", ""))
    if desc:
        lines.append(desc)
        lines.append("")
    rows = []
    for fname, prop in props.items():
        ftype = field_type(prop)
        desc = clean_desc(prop.get("description", ""))
        default = prop.get("default", "")
        required_flag = fname in required
        if fname in K8S_TYPE_MAP:
            k8s_name, k8s_pkg, k8s_url = K8S_TYPE_MAP[fname]
            desc = (desc + " " if desc else "") + (
                f"See Kubernetes [`{k8s_name}`]({k8s_url}) ({k8s_pkg})."
            )
        rows.append((fname, ftype, required_flag, default, desc))
    lines.append(render_table(rows))
    lines.append("")
    # Recurse into nested CodX objects
    for fname, prop in props.items():
        if fname in K8S_TYPE_MAP:
            continue
        if (prop.get("type") == "object" and "properties" in prop
                and fname in TOP_LEVEL_OBJECTS):
            lines.append("")
            lines.extend(doc_object(fname, prop, level=level + 1,
                                    anchor_type=CODEX_OBJECT_TYPES.get(fname, fname)))
    return lines


def main():
    with open(CRD_PATH) as f:
        crd = yaml.safe_load(f)
    meta = crd["metadata"]
    spec = crd["spec"]
    group = spec["group"]
    scope = spec["scope"]
    names = spec["names"]
    version = spec["versions"][0]
    ver_name = version["name"]
    schema = version["schema"]["openAPIV3Schema"]

    out = []
    out.append("# `Profile` CRD Reference")
    out.append("")
    out.append(
        f"<!-- Generated from `{meta['name']}` (group `{group}`, version "
        f"`{ver_name}`). Do not edit by hand; regenerate with `make docs-crd`. -->"
    )
    out.append("")
    out.append("## Overview")
    out.append("")
    out.append("| Property | Value |")
    out.append("|----------|-------|")
    out.append(f"| **Group** | `{group}` |")
    out.append(f"| **Version** | `{ver_name}` |")
    out.append(f"| **Scope** | `{scope}` |")
    out.append(f"| **Kind** | `{names['kind']}` |")
    out.append(f"| **List kind** | `{names['listKind']}` |")
    out.append(f"| **Singular** | `{names['singular']}` |")
    out.append(f"| **Plural** | `{names['plural']}` |")
    out.append(f"| **Short names** | "
               + ", ".join(f"`{s}`" for s in names.get("shortNames", [])) + " |")
    out.append(f"| **Served** | `{version.get('served', True)}` |")
    out.append(f"| **Storage** | `{version.get('storage', True)}` |")
    out.append("| **Subresources** | `status` |")
    out.append("")
    out.append(clean_desc(schema.get("description", "")))
    out.append("")
    out.append("## Example")
    out.append("")
    out.append("```yaml")
    out.append("apiVersion: codx.io/v1")
    out.append("kind: Profile")
    out.append("metadata:")
    out.append("  name: default")
    out.append("spec:")
    out.append("  title: Default")
    out.append("  description: Standard code-server workspace")
    out.append("  inactivityStopDelaySeconds: 1800")
    out.append("  oidcGroups:")
    out.append("    - doca:users")
    out.append("  podSpec:")
    out.append("    image: codercom/code-server:4.93.1")
    out.append("    resources:")
    out.append("      requests:")
    out.append("        cpu: 250m")
    out.append("        memory: 512Mi")
    out.append("  pvc:")
    out.append("    size: 10Gi")
    out.append("```")
    out.append("")

    # spec
    spec_schema = schema["properties"]["spec"]
    out.append("## `spec`")
    out.append("")
    out.append(clean_desc(spec_schema.get("description", "")))
    out.append("")
    spec_props = spec_schema.get("properties", {})
    spec_required = spec_schema.get("required", [])
    rows = []
    for fname, prop in spec_props.items():
        ftype = field_type(prop)
        desc = clean_desc(prop.get("description", ""))
        default = prop.get("default", "")
        rows.append((fname, ftype, fname in spec_required, default, desc))
    out.append(render_table(rows))
    out.append("")
    for fname, prop in spec_props.items():
        if (fname in TOP_LEVEL_OBJECTS and prop.get("type") == "object"
                and "properties" in prop):
            out.append("")
            out.extend(doc_object(fname, prop, level=3,
                                  anchor_type=CODEX_OBJECT_TYPES.get(fname, fname)))
    out.append("")

    # status
    status_schema = schema["properties"].get("status")
    if status_schema:
        out.append("## `status`")
        out.append("")
        out.append(clean_desc(status_schema.get("description", "")))
        out.append("")
        status_props = status_schema.get("properties", {})
        status_required = status_schema.get("required", [])
        rows = []
        for fname, prop in status_props.items():
            ftype = field_type(prop)
            desc = clean_desc(prop.get("description", ""))
            if fname in K8S_TYPE_MAP:
                k8s_name, k8s_pkg, k8s_url = K8S_TYPE_MAP[fname]
                desc = (desc + " " if desc else "") + (
                    f"See Kubernetes [`{k8s_name}`]({k8s_url}) ({k8s_pkg})."
                )
            rows.append((fname, ftype, fname in status_required, "", desc))
        out.append(render_table(rows))
        out.append("")

    out.append("## Notes on embedded Kubernetes types")
    out.append("")
    out.append("Several `podSpec` fields embed upstream Kubernetes core types "
               "(`corev1`/`metav1`). Their full schemas are intentionally not "
               "duplicated here; refer to the linked Kubernetes API reference for "
               "the complete field list:")
    out.append("")
    for fname, (k8s_name, k8s_pkg, k8s_url) in K8S_TYPE_MAP.items():
        out.append(f"- **`{fname}`** -> [`{k8s_name}`]({k8s_url}) (`{k8s_pkg}`)")
    out.append("")
    out.append("## References")
    out.append("")
    out.append("- Source types: [`api/profile/v1/profile_types.go`](../api/profile/v1/profile_types.go)")
    out.append("- CRD manifest: [`charts/codx/crds/codx.io_profiles.yaml`](../charts/codx/crds/codx.io_profiles.yaml)")
    out.append("- Helm template: [`charts/codx/templates/profiles.yaml`](../charts/codx/templates/profiles.yaml)")
    out.append("")

    os.makedirs(os.path.dirname(OUT_PATH), exist_ok=True)
    with open(OUT_PATH, "w") as f:
        f.write("\n".join(out))
    print(f"Wrote {OUT_PATH} ({len(out)} lines)")


if __name__ == "__main__":
    main()
