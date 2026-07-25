/** @type {import('next').NextConfig} */
const nextConfig = {
  // Optional deps pulled in transitively by wagmi/walletconnect/metamask that
  // aren't used in the browser build. Externalizing silences module-not-found
  // warnings without affecting the injected-connector path we actually use.
  webpack: (config) => {
    config.externals.push(
      "pino-pretty",
      "lokijs",
      "encoding",
      "@react-native-async-storage/async-storage",
    );
    return config;
  },
};

export default nextConfig;
