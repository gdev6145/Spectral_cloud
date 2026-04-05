#!/usr/bin/env bash
set -euo pipefail

OUT_DIR="${1:-certs}"
CN_SERVER="${2:-spectral-cloud}"
CN_CLIENT="${3:-spectral-client}"

mkdir -p "${OUT_DIR}"

CA_KEY="${OUT_DIR}/ca.key"
CA_CERT="${OUT_DIR}/ca.pem"
SERVER_KEY="${OUT_DIR}/server.key"
SERVER_CSR="${OUT_DIR}/server.csr"
SERVER_CERT="${OUT_DIR}/server.pem"
CLIENT_KEY="${OUT_DIR}/client.key"
CLIENT_CSR="${OUT_DIR}/client.csr"
CLIENT_CERT="${OUT_DIR}/client.pem"

openssl genrsa -out "${CA_KEY}" 4096
openssl req -x509 -new -nodes -key "${CA_KEY}" -sha256 -days 3650 -out "${CA_CERT}" -subj "/CN=Spectral-Cloud-CA"

openssl genrsa -out "${SERVER_KEY}" 4096
openssl req -new -key "${SERVER_KEY}" -out "${SERVER_CSR}" -subj "/CN=${CN_SERVER}"
openssl x509 -req -in "${SERVER_CSR}" -CA "${CA_CERT}" -CAkey "${CA_KEY}" -CAcreateserial -out "${SERVER_CERT}" -days 365 -sha256

openssl genrsa -out "${CLIENT_KEY}" 4096
openssl req -new -key "${CLIENT_KEY}" -out "${CLIENT_CSR}" -subj "/CN=${CN_CLIENT}"
openssl x509 -req -in "${CLIENT_CSR}" -CA "${CA_CERT}" -CAkey "${CA_KEY}" -CAcreateserial -out "${CLIENT_CERT}" -days 365 -sha256

rm -f "${SERVER_CSR}" "${CLIENT_CSR}" "${OUT_DIR}/ca.srl"

chmod 600 "${CA_KEY}" "${SERVER_KEY}" "${CLIENT_KEY}"

echo "Generated:"
echo "  CA:     ${CA_CERT}"
echo "  Server: ${SERVER_CERT} ${SERVER_KEY}"
echo "  Client: ${CLIENT_CERT} ${CLIENT_KEY}"
