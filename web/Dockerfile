FROM node:22.19.0-alpine3.22 AS builder

WORKDIR /src
COPY web/package.json web/package.json
COPY web/tsconfig.json web/tsconfig.json
COPY web/index.html web/index.html
COPY web/scripts web/scripts
COPY web/src web/src
COPY clients/typescript/generated.ts clients/typescript/generated.ts

WORKDIR /src/web
RUN npm run build

FROM nginx:1.29.1-alpine3.22

COPY web/nginx.conf /etc/nginx/nginx.conf
COPY --from=builder --chown=101:101 /src/web/dist /usr/share/nginx/html

USER 101:101
EXPOSE 8080

ENTRYPOINT ["nginx", "-g", "daemon off;"]
