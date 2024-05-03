"use client";
import SecondaryButton from "@/components/Buttons/SecondaryButton";
import styles from "../../page.module.css";
import { Ubuntu_Mono } from "next/font/google";
import PrimaryButton from "@/components/Buttons/PrimaryButton";
import InputField from "@/components/InputField/InputField";
import { useState, useCallback } from "react";
import { toast } from "react-toastify";

const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function Home() {
  const [registryUrl, setRegistryUrl] = useState("");

  const handleRegistryUrlChange = (event) => {
    setRegistryUrl(event.target.value);
  };

  const installRegistry = useCallback(async () => {
    const toastId = toast.loading("Installing registry...");

    try {
      const response = await toast.promise(
        fetch("/api/install-registry", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ registryUrl: registryUrl }),
        }),
        {
          success: "registry installed successfully 👌",
          error: "Failed to install registry 🤯",
        }
      );
      console.log("console.log of response", response);

      if (response.ok) {
        console.log("Repository cloned successfully");
        toast.update(toastId, {
          render: "Registry installed successfully 👌",
          type: "success",
          isLoading: false,
          autoClose: 5000,
        });
      } else {
        console.error("Failed to clone repository");
        toast.update(toastId, {
          render: "Failed to install registry 🤯",
          type: "error",
          isLoading: false,
          autoClose: 5000,
        });
      }
    } catch (error) {
      console.error("An error occurred:", error);
      toast.update(toastId, {
        render: "An error occurred while installing the registry 😥",
        type: "error",
        isLoading: false,
        autoClose: 5000,
      });
    }
  }, [registryUrl]);

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
          <p className={styles.mainText}>Registry</p>
          <div className={styles.formContainer}>
            <InputField
              label={"Public Registry URL"}
              value={registryUrl}
              onChange={handleRegistryUrlChange}
            />
            <div className={styles.buttonsContainer}>
              {/* <SecondaryButton text={"Cancel"} /> */}
              <PrimaryButton onClick={installRegistry} text={"Continue"} />
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
