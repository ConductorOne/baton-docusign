FROM gcr.io/distroless/static-debian11:nonroot
ENTRYPOINT ["/baton-docusign"]
COPY baton-docusign /