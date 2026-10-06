{{/* vim: set filetype=mustache: */}}

{{/*
Return the proper codx image name
*/}}
{{- define "codx.image" -}}
{{ include "common.images.image" (dict "imageRoot" .Values.image "global" .Values.global) }}
{{- end -}}

{{/*
Return the proper Docker Image Registry Secret Names
*/}}
{{- define "codx.imagePullSecrets" -}}
{{- include "common.images.pullSecrets" (dict "images" .Values.image "global" .Values.global) -}}
{{- end -}}

{{/*
Create the name of the service account to use
*/}}
{{- define "codx.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
    {{ default (include "common.names.fullname" .) .Values.serviceAccount.name }}
{{- else -}}
    {{ default "default" .Values.serviceAccount.name }}
{{- end -}}
{{- end -}}

{{/*
Return true if cert-manager required annotations for TLS signed certificates are set in the Ingress annotations
Ref: https://cert-manager.io/docs/usage/ingress/#supported-annotations
*/}}
{{- define "codx.ingress.certManagerRequest" -}}
{{ if or (hasKey . "cert-manager.io/cluster-issuer") (hasKey . "cert-manager.io/issuer") }}
    {{- true -}}
{{- end -}}
{{- end -}}

{{/*
Return true if a TLS credentials secret object should be created
*/}}
{{- define "codx.createTlsSecret" -}}
{{- if and (not .Values.tls.existingSecret) .Values.tls.enabled }}
    {{- true -}}
{{- end -}}
{{- end -}}

{{/*
Return the TLS secret name
*/}}
{{- define "codx.issuerName" -}}
{{- $issuerName := .Values.tls.issuerRef.existingIssuerName -}}
{{- if $issuerName -}}
    {{- printf "%s" (tpl $issuerName $) -}}
{{- else -}}
    {{- printf "%s-http" (include "common.names.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Instance name: defaults to release name, used in workspace object names.
*/}}
{{- define "codx.instanceName" -}}
{{- if .Values.instanceName -}}
{{- .Values.instanceName -}}
{{- else -}}
{{- .Release.Name -}}
{{- end -}}
{{- end -}}

{{/*
Traefik mTLS: name of the client CA Secret. When traefikMtls.existingCaSecret
is set the user-managed secret is used, otherwise the chart creates one.
*/}}
{{- define "codx.traefikMtls.caSecretName" -}}
{{- $secret := .Values.traefikMtls.existingCaSecret -}}
{{- if $secret -}}
{{- tpl $secret $ -}}
{{- else -}}
{{- printf "%s-client-ca" (include "common.names.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Traefik mTLS: name of the TLSOption requesting the client certificate.
*/}}
{{- define "codx.traefikMtls.tlsOptionName" -}}
{{- .Values.traefikMtls.tlsOption.name | default (printf "%s-client-mtls" (include "common.names.fullname" .)) -}}
{{- end -}}

{{/*
Traefik mTLS: name of the passTLSClientCert Middleware.
*/}}
{{- define "codx.traefikMtls.middlewareName" -}}
{{- .Values.traefikMtls.middleware.name | default (printf "%s-passtlsclientcert" (include "common.names.fullname" .)) -}}
{{- end -}}
