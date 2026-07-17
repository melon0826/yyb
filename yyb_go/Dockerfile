FROM scratch

COPY yyb-go-static /app/yyb-go
COPY resource/ /app/resource/

EXPOSE 8000

ENTRYPOINT ["/app/yyb-go", "--host", "0.0.0.0", "--port", "8000", "--resource-root", "/app/resource"]
