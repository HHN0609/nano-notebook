FROM nginx:1.27-alpine

COPY web/dist /usr/share/nginx/html
COPY infra/nginx/nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
