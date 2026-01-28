import {
  Box,
  Chip,
  Paper,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tooltip,
  Typography,
} from "@mui/material";
import type { DomainInfo } from "../types";

const cdnColors: Record<
  string,
  "primary" | "secondary" | "error" | "info" | "success" | "warning"
> = {
  Cloudflare: "warning",
  Google: "error",
  "Amazon CloudFront": "warning",
  Amazon: "warning",
  AWS: "warning",
  Akamai: "info",
  Fastly: "secondary",
  Microsoft: "info",
  "Microsoft Azure": "info",
  "Microsoft Azure CDN": "info",
  Meta: "primary",
  Apple: "secondary",
  Verizon: "info",
  Limelight: "info",
  StackPath: "secondary",
  "Alibaba Cloud": "warning",
  Tencent: "info",
  Yandex: "error",
  VK: "primary",
  Selectel: "info",
  Gcore: "success",
  "DDoS-Guard": "success",
  DigitalOcean: "info",
  Telegram: "primary",
};

interface ResultsTableProps {
  domains: DomainInfo[] | null;
}

export function ResultsTable({ domains }: Readonly<ResultsTableProps>) {
  const domainList = [...(domains || [])].sort((a, b) => {
    if (a.external === b.external) return 0;
    return a.external ? 1 : -1;
  });

  if (domainList.length === 0) {
    return (
      <Paper sx={{ p: 3, textAlign: "center" }}>
        <Typography color="text.secondary">No domains found</Typography>
      </Paper>
    );
  }

  return (
    <TableContainer component={Paper}>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>Domain</TableCell>
            <TableCell>IP Address</TableCell>
            <TableCell>CDN / Provider</TableCell>
            <TableCell>Type</TableCell>
            <TableCell>Sources</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {domainList.map((domain) => (
            <TableRow key={domain.domain} hover>
              <TableCell>
                <Typography
                  variant="body2"
                  fontFamily="monospace"
                  fontSize="0.8rem"
                >
                  {domain.domain}
                </Typography>
              </TableCell>
              <TableCell>
                {domain.ips && domain.ips.length > 0 ? (
                  <Tooltip title={domain.ips.join(", ")} arrow>
                    <Box>
                      <Typography
                        variant="body2"
                        fontFamily="monospace"
                        fontSize="0.75rem"
                        color="text.secondary"
                      >
                        {domain.ips[0]}
                        {domain.ips.length > 1 && (
                          <Typography
                            component="span"
                            variant="caption"
                            color="primary"
                            sx={{ ml: 0.5 }}
                          >
                            +{domain.ips.length - 1}
                          </Typography>
                        )}
                      </Typography>
                    </Box>
                  </Tooltip>
                ) : (
                  <Typography
                    variant="body2"
                    color="text.disabled"
                    fontSize="0.75rem"
                  >
                    -
                  </Typography>
                )}
              </TableCell>
              <TableCell>
                {domain.cdn ? (
                  <Chip
                    label={domain.cdn}
                    size="small"
                    color={cdnColors[domain.cdn] || "default"}
                    variant="outlined"
                  />
                ) : (
                  <Typography
                    variant="body2"
                    color="text.disabled"
                    fontSize="0.75rem"
                  >
                    -
                  </Typography>
                )}
              </TableCell>
              <TableCell>
                <Chip
                  label={domain.external ? "External" : "Internal"}
                  color={domain.external ? "warning" : "success"}
                  size="small"
                />
              </TableCell>
              <TableCell>
                <Stack direction="row" spacing={0.5} flexWrap="wrap" useFlexGap>
                  {domain.sources.map((source) => (
                    <Chip
                      key={source}
                      label={source}
                      size="small"
                      variant="outlined"
                    />
                  ))}
                </Stack>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </TableContainer>
  );
}
