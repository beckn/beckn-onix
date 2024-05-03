"use client";

import InputField from "@/components/InputField/InputField";
import styles from "../../page.module.css";
import { Ubuntu_Mono } from "next/font/google";
import { useState, useCallback } from "react";
import SecondaryButton from "@/components/Buttons/SecondaryButton";
import PrimaryButton from "@/components/Buttons/PrimaryButton";
import { usePathname } from "next/navigation";
import { toast } from "react-toastify";

const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function Home() {
  let pathname = usePathname();
  const [gatewayUrl, setGatewayUrl] = useState("");
  const [registryUrl, setRegistryUrl] = useState("");
  const [networkconfigurl, setNetworkconfigurl] = useState("");

  const handleGatewayUrlChange = (event) => {
    setGatewayUrl(event.target.value);
  };
  const handleRegistryUrlChange = (event) => {
    setRegistryUrl(event.target.value);
  };
  const handleNetworkconfigurlChange = (event) => {
    setNetworkconfigurl(event.target.value);
  };

  const installGateway = useCallback(async () => {
    const toastId = toast.loading("Installing gateway...");

    try {
      const response = await toast.promise(
        fetch("/api/install-gateway", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({
            gatewayUrl: gatewayUrl,
            registryUrl: registryUrl,
            networkconfigurl: networkconfigurl,
          }),
        }),
        {
          success: "gateway installed successfully 👌",
          error: "Failed to install BAP 🤯",
        }
      );

      if (response.ok) {
        console.log("Gateway installed successfully");
        toast.update(toastId, {
          render: "Gateway installed successfully 👌",
          type: "success",
          isLoading: false,
          autoClose: 5000,
        });
      } else {
        console.error("Failed to install gateway");
        toast.update(toastId, {
          render: "Failed to install gateway 🤯",
          type: "error",
          isLoading: false,
          autoClose: 5000,
        });
      }
    } catch (error) {
      console.error("An error occurred:", error);
      toast.update(toastId, {
        render: "An error occurred while installing the gateway 😥",
        type: "error",
        isLoading: false,
        autoClose: 5000,
      });
    }
  }, [gatewayUrl, registryUrl, networkconfigurl]);
  return (
    <>
      <main className={ubuntuMono.className}>
        <div className={styles.mainContainer}>
          <button
            onClick={() => window.history.back()}
            className={styles.backButton}
          >
            Back
          </button>
          <p className={styles.mainText}>Gateway</p>
          <div className={styles.formContainer}>
            {/* To do todo 
          1. Create a check function so that the url formats are correct
          2. Send response when installing and also erros that happen when an envet happens to the user
          3. a gear dialog where the user's can specify to where the beckn repo to be cloned.
           */}

            <InputField
              label={"Publicly Accessible Gateway URL"}
              value={gatewayUrl}
              onChange={handleGatewayUrlChange}
            />
            <InputField
              label={"Registry URL"}
              value={registryUrl}
              onChange={handleRegistryUrlChange}
            />
            <InputField
              label={"Network Configuration URL"}
              value={networkconfigurl}
              onChange={handleNetworkconfigurlChange}
            />

            <div className={styles.buttonsContainer}>
              {/* <SecondaryButton text={"Cancel"} /> */}
              <PrimaryButton onClick={installGateway} text={"Continue"} />
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
