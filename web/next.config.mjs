/** @type {import('next').NextConfig} */
const nextConfig = {
  // Emit a self-contained server (server.js + traced node_modules) for a small
  // container image. See web/Dockerfile.
  output: "standalone",
  reactStrictMode: true,
  devIndicators: false,
};

export default nextConfig;
