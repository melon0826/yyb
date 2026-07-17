#!/bin/sh
exec ./yyb-go \
  --host 0.0.0.0 \
  --port "${YYB_PORT:-8000}" \
  --resource-root ./resource
