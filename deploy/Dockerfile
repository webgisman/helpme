# Relais HelpMe : binaire Go statique (compilé par deploy/build.sh), image minimale sans shell, utilisateur non-root.
FROM scratch
COPY helpme-relay /helpme-relay
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/helpme-relay", "-addr", ":8080", "-origins", "guxos.com,www.guxos.com"]
